-- Independently authored jastreamer adapter. Control RPC uses the native RAAT
-- session; authenticated PCM has a bounded, backpressured TCP connection.
local pack, unpack = string.pack, string.unpack
local output = raat.output or {}
local transport = raat.transport or {}
local source = raat.source_selection or {}
local MAX_PCM, MAX_ARTWORK, MAX_TEXT = 65536, 3 * 1024 * 1024, 65536
local MAX_BUFFER, MAX_EVENTS = 16 * 1024 * 1024, 64
local MAX_AUDIO_BUFFER, AUDIO_TOKEN_BYTES = 2 * MAX_PCM + 64, 32
local BUS_IP, BUS_PORT = "239.255.90.91", 9004
local types = { [1] = "pcm", [2] = "pcm_float32", [3] = "pcm_float64" }
local type_ids = { pcm = 1, pcm_float32 = 2, pcm_float64 = 3 }
local current, subscription, pending_claim, bus, timer
local generation, claim_generation = 0, 0
local control_epoch = 0
local owned, announcement, publishing, closed = false, nil, false, false
local controller_id
local events, event_first, event_last = {}, 1, 0
local pending = {}
local stopped_position = 0
local shutdown, lose

local function reason(value)
    -- Error messages must remain safe UTF-8 even if a native exception is binary.
    return tostring(value or "native operation failed"):sub(1, 1024):gsub("[^\032-\126]", "?")
end

local function integer(value, label, minimum, maximum)
    local n = math.tointeger(value)
    if not n or n < minimum or (maximum and n > maximum) then
        error("unsupported or invalid " .. label, 0)
    end
    return n
end

local function text(value, maximum)
    if #value > maximum or value:find("\0", 1, true) or not utf8.len(value) then
        error("invalid UTF-8 text or text too large", 0)
    end
    return value
end

-- The ownership bus has only a flat object of string/null fields. Decode that
-- grammar locally so a new owner's status takes effect before any TCP roundtrip.
local function bus_message(payload)
    text(payload, 4096)
    local index, size = 1, #payload
    local function space()
        while index <= size and payload:sub(index, index):match("[ \t\r\n]") do index = index + 1 end
    end
    local function quoted()
        if payload:sub(index, index) ~= '"' then error("invalid ownership JSON", 0) end
        index = index + 1
        local parts, first = {}, index
        while index <= size do
            local byte = payload:byte(index)
            if byte == 34 then
                parts[#parts + 1] = payload:sub(first, index - 1)
                index = index + 1
                return table.concat(parts)
            elseif byte == 92 then
                parts[#parts + 1] = payload:sub(first, index - 1)
                index = index + 1
                local escaped = payload:sub(index, index)
                local replacements = { ['"'] = '"', ['\\'] = '\\', ['/'] = '/',
                    b = '\b', f = '\f', n = '\n', r = '\r', t = '\t' }
                if escaped == "u" then
                    local function hex()
                        local digits = payload:sub(index + 1, index + 4)
                        if not digits:match("^%x%x%x%x$") then error("invalid ownership escape", 0) end
                        index = index + 5
                        return tonumber(digits, 16)
                    end
                    local code = hex()
                    if code >= 0xd800 and code <= 0xdbff then
                        if payload:sub(index, index + 1) ~= "\\u" then error("invalid ownership surrogate", 0) end
                        index = index + 1
                        local low = hex()
                        if low < 0xdc00 or low > 0xdfff then error("invalid ownership surrogate", 0) end
                        code = 0x10000 + (code - 0xd800) * 0x400 + low - 0xdc00
                    elseif code >= 0xdc00 and code <= 0xdfff then
                        error("invalid ownership surrogate", 0)
                    end
                    parts[#parts + 1] = utf8.char(code)
                elseif replacements[escaped] then
                    parts[#parts + 1] = replacements[escaped]
                    index = index + 1
                else error("invalid ownership escape", 0) end
                first = index
            elseif byte < 32 then error("invalid ownership string", 0)
            else index = index + 1 end
        end
        error("unterminated ownership string", 0)
    end
    space()
    if payload:sub(index, index) ~= "{" then error("invalid ownership object", 0) end
    index = index + 1
    local result, seen = {}, {}
    while true do
        space()
        local key = quoted()
        if (key ~= "command" and key ~= "owner_id") or seen[key] then error("invalid ownership field", 0) end
        seen[key] = true
        space()
        if payload:sub(index, index) ~= ":" then error("invalid ownership field", 0) end
        index = index + 1
        space()
        if payload:sub(index, index + 3) == "null" then index = index + 4
        else result[key] = quoted() end
        space()
        local separator = payload:sub(index, index)
        index = index + 1
        if separator == "}" then break end
        if separator ~= "," then error("invalid ownership separator", 0) end
    end
    space()
    if index ~= size + 1 or (result.command ~= "query" and result.command ~= "status") then
        error("invalid ownership message", 0)
    end
    return result
end

local function call(fn, ...)
    if type(fn) ~= "function" then error("unsupported native operation", 0) end
    return fn(...)
end

local function send(id, final, code, data)
    if closed then return end
    local body = pack(">I4BB", id, final and 1 or 0, code) .. (data or "")
    local ok, err = pcall(raat.session.send_message, 2, buffer.new(body))
    if not ok then
        shutdown()
        error("session send failed: " .. reason(err), 0)
    end
end

local function finish(cx, code, data)
    if cx.done then return end
    cx.done = true
    if pending[cx.id] == cx then pending[cx.id] = nil end
    send(cx.id, true, code, data)
end

-- luv exits the device process on an uncaught callback exception. Error handling
-- itself is protected, including failed control sends during loss/cleanup.
local function callback(fn, s)
    return function(...)
        local ok, err = pcall(fn, ...)
        if not ok and not closed then
            local handled = false
            if s and current == s then handled = pcall(lose, s, err) end
            if not handled then pcall(shutdown) end
        end
    end
end

local function close_handle(connection)
    if not connection or connection.closed then return end
    connection.closed, connection.reading = true, false
    pcall(function() connection.handle:read_stop() end)
    pcall(function() connection.handle:close() end)
end

local function close_audio(s)
    close_handle(s.listener)
    close_handle(s.candidate)
    close_handle(s.audio)
    s.listener, s.candidate, s.audio, s.audio_buffer = nil, nil, nil, ""
end

local function cancel_start(s, message)
    local cx = s.pending_start
    s.pending_start = nil
    if cx and not closed then finish(cx, 1, message) end
end

local function clear_events()
    events, event_first, event_last = {}, 1, 0
end

local function event(code, data)
    if not subscription or closed then return end
    if event_last - event_first + 1 >= MAX_EVENTS then
        local cx = subscription
        subscription = nil
        clear_events()
        finish(cx, 1, "event queue overflow; subscription closed")
        if current then lose(current, "event queue overflow") end
        return
    end
    event_last = event_last + 1
    events[event_last] = { code, data }
end

local function snapshot(s)
    if not s then
        return pack(">Bi8i8i8i8i8i8i8i8i8i8i8", 5, stopped_position, 0, 0, 0, 0, 0, 0, 0, 0, 0, control_epoch)
    end
    return pack(">Bi8i8i8i8i8i8i8i8i8i8i8", s.state, s.position, s.written, s.samples_out,
        s.fill, s.capacity, s.dropped, s.overrun, s.delay, s.time, s.start, control_epoch)
end

local function dispose(s)
    local token = s.token
    s.token, s.stream, s.disposing = nil, nil, true
    close_audio(s)
    local cancelled, cancel_error = pcall(cancel_start, s, "stream closed before start")
    if token == nil then return end
    local stop_ok, stop_error = true, nil
    if s.started then stop_ok, stop_error = pcall(call, output.stop, token) end
    s.started = false
    local teardown_ok, teardown_error = pcall(call, output.teardown, token)
    if not teardown_ok then error("teardown failed: " .. reason(teardown_error), 0) end
    if not stop_ok then error("stop failed: " .. reason(stop_error), 0) end
    if not cancelled then error(reason(cancel_error), 0) end
end

lose = function(s, message)
    if current ~= s or s.state == 6 then return end
    s.state = 6
    generation = generation + 1
    if s.setup then finish(s.setup, 1, reason(message)); s.setup = nil end
    -- Invalidate before native teardown, which may synchronously report loss.
    local ok, err = pcall(dispose, s)
    if not ok then message = reason(message) .. "; " .. reason(err) end
    event(6, reason(message))
end

local function refresh(s)
    if not s.stream or s.token == nil then return end
    local stats = s.stream:get_stats(s.stats)
    if type(stats) ~= "table" then error("unsupported native stream counters", 0) end
    local capacity = integer(stats.capacity_samples, "capacity_samples counter", 1)
    local fill = integer(stats.fill_samples, "fill_samples counter", 0, capacity)
    local out = integer(stats.samples_out, "samples_out counter", 0)
    local dropped = integer(stats.samples_dropped, "samples_dropped counter", 0, out)
    local overrun = integer(stats.samples_overrun, "samples_overrun counter", 0)
    if capacity ~= s.capacity or out < s.samples_out or dropped < s.dropped or overrun < s.overrun then
        error("native stream counters changed unexpectedly", 0)
    end
    s.fill, s.samples_out, s.dropped, s.overrun = fill, out, dropped, overrun
    s.time = integer(call(output.get_local_time, s.token), "device clock", math.mininteger)
    if output.is_get_output_delay_supported then
        local delay = call(output.get_output_delay, s.token)
        s.delay = delay == nil and 0 or integer(delay, "output delay", 0)
    else
        s.delay = 0
    end
    if overrun ~= 0 then error("native stream overrun", 0) end
    local tail = s.eof and math.max(0, out - s.written) or 0
    if dropped > tail then error("native stream dropped source frames", 0) end
    if s.state == 2 then
        local elapsed = math.max(0, s.time - s.start - s.delay)
        local seconds, remainder = elapsed // 1000000000, elapsed % 1000000000
        local position = math.min(s.written, seconds * s.rate + remainder * s.rate // 1000000000)
        s.position = math.max(s.position, math.min(position, out - dropped))
        local duration = (s.written // s.rate) * 1000000000
            + ((s.written % s.rate) * 1000000000 + s.rate - 1) // s.rate
        if s.eof and out - dropped >= s.written and s.time - s.start >= duration + s.delay then
            s.position, s.state = s.written, 4
            dispose(s)
        end
    end
end

local function checked_refresh(s)
    local ok, err = pcall(refresh, s)
    if not ok then lose(s, err); error(reason(err), 0) end
end

local function active()
    if not current or not current.stream or current.token == nil then
        error("no active output stream", 0)
    end
    return current
end

local function uv_result(result, err)
    if result == nil or result == false or (type(result) == "number" and result < 0) then
        error("audio socket operation failed: " .. reason(err), 0)
    end
    return result
end

local function try_start(s)
    local cx = s.pending_start
    if not cx then return end
    refresh(s)
    if s.fill < s.ready and not (s.eof and s.fill > 0) then return end
    if s.time > math.maxinteger - 250000000 then error("device clock overflow", 0) end
    s.start = s.time + 250000000
    s.started = true
    call(output.start, s.token, s.start, 0, s.stream)
    if current ~= s or s.state == 6 then error("output lost while starting", 0) end
    s.state, s.pending_start = 2, nil
    finish(cx, 0, snapshot(s))
end

local function audio_live(s)
    return not closed and current == s and generation == s.generation
        and s.token ~= nil and (s.state == 1 or s.state == 2)
end

local function drain_audio(s)
    while #s.audio_buffer > 0 do
        if s.eof then error("audio bytes after EOF", 0) end
        local opcode = s.audio_buffer:byte(1)
        if opcode == 7 then
            if s.written == 0 then error("empty stream", 0) end
            -- A pre-EOF dropout must never be mistaken for native tail zero-fill.
            refresh(s)
            s.eof, s.audio_buffer = true, s.audio_buffer:sub(2)
        elseif opcode == 3 then
            if #s.audio_buffer < 13 then return false end
            local offset, frames = unpack(">i8I4", s.audio_buffer, 2)
            if offset ~= s.written or frames == 0 then error("nonsequential PCM packet", 0) end
            local count = frames * s.frame_bytes
            if count > MAX_PCM or frames > s.capacity then error("invalid PCM byte count", 0) end
            if s.written > (math.maxinteger // 1000000000 - 1) * s.rate - frames then
                error("stream duration exceeds native clock range", 0)
            end
            if #s.audio_buffer < 13 + count then return false end
            refresh(s)
            if frames > s.capacity - s.fill then return true end
            s.stream:write(offset, 1.0, 1.0, s.audio_buffer:sub(14, 13 + count), frames)
            s.written = s.written + frames
            s.audio_buffer = s.audio_buffer:sub(14 + count)
            refresh(s)
        else
            error("unsupported audio opcode", 0)
        end
    end
    return false
end

local function pump_audio(s)
    if not audio_live(s) then return end
    local blocked = drain_audio(s)
    try_start(s)
    local connection = s.audio
    if not connection or connection.closed then return end
    if blocked and connection.reading then
        uv_result(connection.handle:read_stop())
        connection.reading = false
    elseif not blocked and not connection.reading then
        uv_result(connection.handle:read_start(connection.on_read))
        connection.reading = true
    end
end

local function listen_audio(s)
    local listener = { handle = uv.new_tcp() }
    s.listener = listener
    uv_result(listener.handle:bind("0.0.0.0", 0))
    local address = uv_result(listener.handle:getsockname())
    local port = integer(address.port, "audio port", 1, 65535)
    uv_result(listener.handle:listen(1, callback(function(err)
        if not audio_live(s) or listener.closed or s.listener ~= listener then return end
        if err then error(err, 0) end
        local connection = { handle = uv.new_tcp(), buffer = "", ticks = 0 }
        local accepted, failure = pcall(function() uv_result(listener.handle:accept(connection.handle)) end)
        if not accepted then close_handle(connection); error(failure, 0) end
        if s.candidate or s.audio then close_handle(connection); return end
        s.candidate = connection
        connection.on_read = callback(function(read_error, chunk)
            if not audio_live(s) or connection.closed
                or (s.candidate ~= connection and s.audio ~= connection) then return end
            if read_error or chunk == nil then
                if s.candidate == connection then
                    s.candidate = nil
                    close_handle(connection)
                    return
                end
                if read_error or not s.eof or #s.audio_buffer ~= 0 then
                    error(read_error or "audio connection closed before EOF", 0)
                end
                close_handle(connection)
                return
            end
            if type(chunk) ~= "string" then error("invalid audio read", 0) end
            if s.candidate == connection then
                local missing = AUDIO_TOKEN_BYTES - #connection.buffer
                connection.buffer = connection.buffer .. chunk:sub(1, missing)
                if #connection.buffer < AUDIO_TOKEN_BYTES then return end
                if connection.buffer ~= s.audio_key then
                    s.candidate = nil
                    close_handle(connection)
                    return
                end
                s.candidate, s.audio, s.audio_key = nil, connection, nil
                connection.buffer = nil
                close_handle(listener)
                s.listener = nil
                chunk = chunk:sub(missing + 1)
            end
            if #chunk > MAX_AUDIO_BUFFER - #s.audio_buffer then error("audio receive buffer exceeded", 0) end
            s.audio_buffer = s.audio_buffer .. chunk
            pump_audio(s)
        end, s)
        uv_result(connection.handle:read_start(connection.on_read))
        connection.reading = true
    end, s)))
    return port
end

local function formats()
    if type(output.supported_formats) ~= "table" then
        error("unsupported native format enumeration", 0)
    end
    local list = {}
    for _, f in pairs(output.supported_formats) do
        if type(f) == "table" and type_ids[f.sample_type] then
            local rate = math.tointeger(f.sample_rate)
            local bits = math.tointeger(f.bits_per_sample)
            local channels = math.tointeger(f.channels)
            if rate and rate > 0 and rate <= 1536000 and channels and channels > 0 and channels <= 32
                and ((f.sample_type == "pcm" and (bits == 16 or bits == 24 or bits == 32))
                or (f.sample_type == "pcm_float32" and bits == 32)
                or (f.sample_type == "pcm_float64" and bits == 64)) then
                list[#list + 1] = { sample_type = f.sample_type, sample_rate = rate,
                    bits_per_sample = bits, channels = channels }
                if #list > 4096 then error("native format list too large", 0) end
            end
        end
    end
    return list
end

local function no_args(args)
    if #args ~= 0 then error("unexpected request arguments", 0) end
end

local function cancel_claim(message)
    claim_generation = claim_generation + 1
    if pending_claim then
        local cx = pending_claim
        pending_claim = nil
        finish(cx, 1, message)
    end
end

local function bus_send(payload, callback)
    if not bus then error("ownership bus unavailable", 0) end
    local result, err = bus:send(payload, BUS_IP, BUS_PORT, callback)
    if result == nil or result == false or (type(result) == "number" and result < 0) then
        error("ownership announcement failed: " .. reason(err), 0)
    end
end

local handlers = {}

handlers[1] = function(cx, args)
    no_args(args)
    local features = 0
    if output.is_supported then features = features | 1 end
    if transport.is_supported then features = features | 2 end
    if transport.is_supported and transport.is_update_status_supported then features = features | 4 end
    if transport.is_supported and transport.is_update_artwork_supported then features = features | 8 end
    if output.is_get_output_delay_supported then features = features | 16 end
    if source.is_supported then features = features | 32 end
    local list = output.is_supported and formats() or {}
    local parts = { pack(">I4I4", features, #list) }
    for _, f in ipairs(list) do
        parts[#parts + 1] = pack(">BI4I2I2", type_ids[f.sample_type], f.sample_rate,
            f.bits_per_sample, f.channels)
    end
    finish(cx, 0, table.concat(parts))
end

handlers[2] = function(cx, args)
    if #args ~= 17 + AUDIO_TOKEN_BYTES then error("invalid setup argument length", 0) end
    if not output.is_supported then error("output unsupported", 0) end
    local kind, rate, bits, channels, capacity, ready = unpack(">BI4I2I2I4I4", args)
    local format
    for _, f in ipairs(formats()) do
        if f.sample_type == types[kind] and f.sample_rate == rate
            and f.bits_per_sample == bits and f.channels == channels then format = f; break end
    end
    if not format then error("unsupported sample format", 0) end
    local frame_bytes = channels * (bits // 8)
    if capacity == 0 or capacity > MAX_BUFFER // frame_bytes or capacity > rate * 10
        or ready == 0 or ready > capacity then error("invalid stream buffer bounds", 0) end
    if type(output.setup) ~= "function" or type(output.start) ~= "function"
        or type(output.stop) ~= "function" or type(output.teardown) ~= "function"
        or type(output.get_local_time) ~= "function" then error("output lifecycle unsupported", 0) end
    control_epoch = control_epoch + 1
    generation = generation + 1
    local mine = generation
    local old = current
    current = nil
    if old then
        if old.setup then finish(old.setup, 1, "setup replaced"); old.setup = nil end
        dispose(old)
    end
    local s = { state = 1, position = 0, written = 0, samples_out = 0, fill = 0,
        capacity = capacity, dropped = 0, overrun = 0, delay = 0, time = 0, start = 0,
        rate = rate, ready = ready, frame_bytes = frame_bytes, stats = {}, setup = cx,
        generation = mine, audio_key = args:sub(18), audio_buffer = "" }
    current = s
    local ok, err = pcall(output.setup, format, {
        on_setup = callback(function(success, token_or_error)
            if closed or generation ~= mine or current ~= s or s.setup ~= cx then
                -- A stale callback can release its own token, never a newer one.
                if success and token_or_error ~= nil and (not current or current.token ~= token_or_error) then
                    pcall(call, output.teardown, token_or_error)
                end
                return
            end
            if not success or token_or_error == nil then
                s.setup = nil
                finish(cx, 1, reason(token_or_error or "setup returned no token"))
                lose(s, token_or_error or "setup failed")
                return
            end
            s.token = token_or_error
            local created, failure = pcall(function()
                s.stream = stream.new(format, capacity)
                refresh(s)
                if s.samples_out ~= 0 or s.fill ~= 0 or s.dropped ~= 0 or s.overrun ~= 0 then
                    error("new stream counters are not zero", 0)
                end
                s.audio_port = listen_audio(s)
            end)
            if not created then lose(s, failure); return end
            s.setup = nil
            finish(cx, 0, pack(">I2", s.audio_port) .. snapshot(s))
        end, s),
        on_output_lost = callback(function(message)
            if not closed and not s.disposing and generation == mine and current == s then
                lose(s, message or "output lost")
            end
        end, s),
    })
    if not ok then lose(s, err) end
end


handlers[4] = function(cx, args)
    no_args(args)
    local s = active()
    if s.state ~= 1 then error("start requires a newly prepared stream", 0) end
    if s.pending_start then error("start already pending", 0) end
    s.pending_start = cx
    local ok, err = pcall(try_start, s)
    if not ok then lose(s, err) end
end

handlers[5] = function(cx, args)
    no_args(args)
    local s = active()
    if s.state ~= 1 and s.state ~= 2 then error("pause requires ready or playing output", 0) end
    checked_refresh(s)
    if s.state == 2 then
        local ok, err = pcall(output.stop, s.token)
        if not ok then lose(s, err); error(reason(err), 0) end
        if s.state == 6 then error("output lost while pausing", 0) end
    elseif s.state ~= 1 then
        error("output already completed", 0)
    end
    s.state, s.started = 3, false
    close_audio(s)
    cancel_start(s, "stream paused before start")
    finish(cx, 0, snapshot(s))
end

handlers[6] = function(cx, args)
    no_args(args)
    control_epoch = control_epoch + 1
    local s = current
    generation = generation + 1
    if s then
        if s.setup then finish(s.setup, 1, "setup stopped"); s.setup = nil end
        local ok, err = pcall(refresh, s)
        stopped_position = s.position
        current = nil
        local disposed, failure = pcall(dispose, s)
        if not disposed then error(reason(failure), 0) end
        if not ok then error(reason(err), 0) end
    end
    finish(cx, 0, snapshot(nil))
end


local function publish(fn, ...)
    publishing = true
    local ok, err = pcall(fn, ...)
    publishing = false
    if not ok then error(reason(err), 0) end
end

handlers[8] = function(cx, args)
    if not owned then finish(cx, 7); return end
    if not transport.is_update_status_supported or type(transport.update_status) ~= "function" then
        error("status publication unsupported", 0)
    end
    text(args, MAX_TEXT)
    if #args == 0 then error("empty status", 0) end
    publish(transport.update_status, args)
    finish(cx, 0)
end

handlers[9] = function(cx, args)
    if not owned then finish(cx, 7); return end
    if not transport.is_update_artwork_supported or type(transport.update_artwork) ~= "function" then
        error("artwork publication unsupported", 0)
    end
    if #args > MAX_ARTWORK then error("artwork too large", 0) end
    if #args == 0 then publish(transport.update_artwork, nil)
    else
        if #args < 4 or args:sub(1, 2) ~= "\255\216" or args:sub(-2) ~= "\255\217" then
            error("artwork must be JPEG", 0)
        end
        publish(transport.update_artwork, "image/jpeg", args)
    end
    finish(cx, 0)
end

handlers[10] = function(cx, args)
    no_args(args)
    if subscription then finish(subscription, 0) end
    clear_events()
    subscription = cx
    send(cx.id, false, 3, snapshot(current))
end

handlers[11] = function(cx, args)
    no_args(args)
    if current then checked_refresh(current) end
    finish(cx, 0, snapshot(current))
end

handlers[12] = function(cx, args)
    local owner = bus_message(args)
    if owner.command ~= "status" or not owner.owner_id or owner.owner_id == "" then
        error("missing ownership identity", 0)
    end
    if transport.is_supported and not bus then error("ownership bus unavailable", 0) end
    cancel_claim("source request replaced")
    local mine = claim_generation
    pending_claim = cx
    local source_answered = false
    local function selected(success, err)
        if closed or source_answered or claim_generation ~= mine or pending_claim ~= cx then return end
        source_answered = true
        if not success then pending_claim = nil; finish(cx, 1, reason(err or "source request failed")); return end
        if not transport.is_supported then pending_claim = nil; finish(cx, 0); return end
        local ok, failure = pcall(bus_send, args, callback(function(send_error)
            if closed or claim_generation ~= mine or pending_claim ~= cx then return end
            pending_claim = nil
            if send_error then finish(cx, 1, "ownership announcement failed: " .. reason(send_error)); return end
            announcement, controller_id, owned = args, owner.owner_id, true
            finish(cx, 0)
        end))
        if not ok and pending_claim == cx then pending_claim = nil; finish(cx, 1, reason(failure)) end
    end
    selected = callback(selected)
    if source.is_supported then
        local ok, err
        if type(source.request_source_v2) == "function" then
            ok, err = pcall(source.request_source_v2, "{}", selected)
        elseif type(source.request_source) == "function" then
            ok, err = pcall(source.request_source, selected)
        else
            ok, err = false, "source selection unsupported"
        end
        if not ok and pending_claim == cx then pending_claim = nil; finish(cx, 1, reason(err)) end
    else selected(true) end
end

handlers[13] = function(cx, args)
    no_args(args)
    if not owned or not announcement then error("transport not owned", 0) end
    local mine = claim_generation
    bus_send(announcement, callback(function(err)
        if cx.done or closed then return end
        if mine ~= claim_generation or not owned then finish(cx, 1, "transport ownership changed")
        elseif err then finish(cx, 1, "ownership announcement failed: " .. reason(err))
        else finish(cx, 0) end
    end))
end


local function on_control(payload)
    if closed or not owned or publishing or type(payload) ~= "string" then return end
    local ok = pcall(text, payload, MAX_TEXT)
    if ok then event(4, pack(">i8", control_epoch) .. payload)
    elseif current then lose(current, "invalid native transport event") end
end

local function on_bus(err, payload)
    if closed or err or payload == nil then return end
    if type(payload) ~= "string" then return end
    local ok, message = pcall(bus_message, payload)
    if not ok then return end
    if message.command == "status" then
        if message.owner_id ~= controller_id then owned = false end
        event(5, payload)
    elseif owned and announcement then
        pcall(bus_send, announcement, function() end)
    end
end

local function on_message(message_type, message_body)
    if closed or message_type ~= 1 then return end
    local ok, buf = pcall(buffer.new, message_body)
    if not ok then shutdown(); return end
    local length = buf:length()
    if length < 5 or length > MAX_ARTWORK + 5 then shutdown(); return end
    local id = unpack(">I4", buf:get_string(0, 4))
    if pending[id] then shutdown(); return end
    local count = 0
    for _ in pairs(pending) do count = count + 1 end
    if count >= MAX_EVENTS then shutdown(); return end
    local cx = { id = id }
    pending[id] = cx
    local opcode = buf:get_uint8(4)
    local handler = handlers[opcode]
    if not handler then finish(cx, 1, "unsupported opcode"); return end
    local success, err = pcall(handler, cx, buf:get_string(5, length - 5))
    if not success then finish(cx, 1, reason(err)) end
end

local function tick()
    if closed then return end
    if current and current.stream then
        local s = current
        local ok, err = pcall(function()
            refresh(s)
            if s.candidate then
                s.candidate.ticks = s.candidate.ticks + 1
                if s.candidate.ticks >= 250 then
                    close_handle(s.candidate)
                    s.candidate = nil
                end
            end
            pump_audio(s)
        end)
        if not ok then lose(s, err) end
    end
    local cx = subscription
    if not cx then return end
    -- Bound work per tick and storage independently; never queue PCM here.
    local remaining = 16
    while subscription == cx and event_first <= event_last and remaining > 0 do
        local item = events[event_first]
        events[event_first], event_first = nil, event_first + 1
        send(cx.id, false, item[1], item[2])
        remaining = remaining - 1
    end
    if event_first > event_last then clear_events() end
    if subscription == cx then send(cx.id, false, 3, snapshot(current)) end
end

shutdown = function()
    if closed then return end
    -- Detach every callback generation before invoking any native cleanup.
    generation, claim_generation = generation + 1, claim_generation + 1
    publishing = true
    if owned then
        if transport.is_update_status_supported and type(transport.update_status) == "function" then
            pcall(transport.update_status, '{"state":"stopped","loop":"disabled","shuffle":false,"is_previous_allowed":false,"is_next_allowed":false,"is_pause_allowed":false,"is_play_allowed":false,"is_seek_allowed":false}')
        end
        if transport.is_update_artwork_supported and type(transport.update_artwork) == "function" then
            pcall(transport.update_artwork, nil)
        end
    end
    owned, announcement = false, nil
    -- Mark closed before cleanup: neither output callbacks nor failed sends recurse.
    closed = true
    if timer then
        pcall(function() timer:stop() end)
        pcall(function() timer:close() end)
        timer = nil
    end
    if bus then
        pcall(uv.udp_recv_stop, bus)
        pcall(function() bus:close() end)
        bus = nil
    end
    local s = current
    current = nil
    if s then pcall(dispose, s) end
    -- The native transport is gone; shutdown abandons pending requests without sending.
    pending, pending_claim, subscription = {}, nil, nil
    clear_events()
end

if transport.is_supported then
    local ok = pcall(function()
        bus = uv.new_udp()
        local result, err = bus:bind("0.0.0.0", BUS_PORT, { reuseaddr = true })
        if result == nil or result == false or (type(result) == "number" and result < 0) then
            error(err or "bus bind failed", 0)
        end
        result, err = bus:set_membership(BUS_IP, "0.0.0.0", "join")
        if result == nil or result == false or (type(result) == "number" and result < 0) then
            error(err or "bus membership failed", 0)
        end
        result, err = bus:set_multicast_ttl(0)
        if result == nil or result == false or (type(result) == "number" and result < 0) then
            error(err or "bus TTL failed", 0)
        end
        result, err = uv.udp_recv_start(bus, callback(on_bus))
        if result == nil or result == false or (type(result) == "number" and result < 0) then
            error(err or "bus receive failed", 0)
        end
    end)
    if not ok and bus then pcall(function() bus:close() end); bus = nil end
    if type(transport.control_handlers) == "table" then
        table.insert(transport.control_handlers, callback(on_control))
    end
end

local initialized, initialization_error = pcall(function()
    timer = uv.new_timer()
    local result, err = timer:start(20, 20, callback(tick))
    if result == nil or result == false or (type(result) == "number" and result < 0) then
        error(err or "timer start failed", 0)
    end
end)
if not initialized then shutdown(); error("receiver timer unavailable: " .. reason(initialization_error), 0) end

table.insert(raat.session.message_handlers, callback(on_message))
table.insert(raat.session.shutdown_handlers, callback(shutdown))
