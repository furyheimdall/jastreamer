local adapter = assert(arg and arg[1], 'pass the receiver.lua path')
local pack, unpack = string.pack, string.unpack
local checks = 0
local function check(ok, why) assert(ok, why); checks = checks + 1 end
local native, replies, timers, udp
local function reset()
  native = { now=1000000000, delay=20000000, stops=0, teardowns=0, statuses={}, artworks={}, tokens={}, announcements={}, sockets={} }
  replies, timers, udp = {}, {}, nil
  buffer = { new=function(value)
    if type(value)=='table' then value=value.bytes end
    if type(value)=='number' then value=string.rep('\0',value) end
    assert(type(value)=='string')
    return { bytes=value, length=function(self)return #self.bytes end,
      get_string=function(self,off,n)return self.bytes:sub(off+1,off+n)end,
      get_uint8=function(self,off)return self.bytes:byte(off+1)end }
  end }
  uv = { new_timer=function()
    local t={}; timers[#timers+1]=t
    function t:start(_,_,cb) self.callback=cb; return 0 end
    function t:stop() self.callback=nil; return 0 end
    function t:close() self.closed=true; return 0 end
    return t
  end, new_tcp=function()
    local sock={read_starts=0,read_stops=0,close_count=0};native.sockets[#native.sockets+1]=sock
    function sock:bind(ip,port)
      assert(ip=='0.0.0.0' and port==0)
      if native.bind_error then return nil,'bind failed' end
      return 0
    end
    function sock:getsockname()return {ip='0.0.0.0',port=41016}end
    function sock:listen(backlog,cb)assert(backlog==1);self.accept_callback=cb;native.listener=self;return 0 end
    function sock:accept(client)
      assert(not self.closed);native.accepted=client
      if native.accept_error then error('native accept exploded')end
      return 0
    end
    function sock:read_start(cb)
      assert(not self.closed);self.callback=cb;self.reading=true;self.read_starts=self.read_starts+1;return 0
    end
    function sock:read_stop()self.reading=false;self.read_stops=self.read_stops+1;return 0 end
    function sock:close()
      assert(not self.closed,'double close');self.closed=true;self.close_count=self.close_count+1
      if native.close_error then error('native close error') end
    end
    function sock:deliver(data,err)
      assert(self.reading and not self.closed,'delivery requires active read')
      self.callback(err,data)
    end
    return sock
  end, new_udp=function()
    local u={}; udp=u
    function u:bind()return 0 end
    function u:set_membership()return 0 end
    function u:set_multicast_ttl(ttl)assert(ttl==0);return 0 end
    function u:send(data,ip,port,cb)
      assert(ip=='239.255.90.91' and port==9004)
      native.announcements[#native.announcements+1]=data; cb(nil); return true
    end
    function u:close()self.closed=true;return 0 end
    return u
  end, udp_recv_start=function(u,cb)u.callback=cb;return 0 end,
  udp_recv_stop=function(u)u.callback=nil;return 0 end }
  stream={new=function(format,capacity)
    local s={format=format, capacity=capacity, chunks={}, stats={write_count=0,read_count=0,samples_in=0,samples_out=0,dropout_count=0,samples_dropped=0,overrun_count=0,samples_overrun=0,capacity_samples=capacity,fill_samples=0}}
    function s:get_stats(reuse)reuse=reuse or {};for k,v in pairs(self.stats)do reuse[k]=v end;return reuse end
    function s:write(offset,gain,peak,data,n)
      assert(type(data)=='string' and gain==1.0 and peak==1.0)
      assert(#data==n*self.format.channels*self.format.bits_per_sample//8)
      assert(self.stats.fill_samples+n<=self.capacity)
      self.chunks[#self.chunks+1]={offset=offset,data=data,frames=n}
      self.stats.fill_samples=self.stats.fill_samples+n;self.stats.samples_in=self.stats.samples_in+n
      self.stats.write_count=self.stats.write_count+1
    end
    native.stream=s;return s
  end}
  local out={is_supported=true,is_get_output_delay_supported=true,
    supported_formats={{sample_type='pcm',sample_rate=48000,bits_per_sample=16,channels=2},
      {sample_type='pcm',sample_rate=48000,bits_per_sample=24,channels=2},
      {sample_type='pcm_float32',sample_rate=48000,bits_per_sample=32,channels=2},
      {sample_type='pcm',sample_rate=48000,bits_per_sample=8,channels=2}}}
  function out.setup(format,callbacks)
    local token={callbacks=callbacks,format=format};native.tokens[#native.tokens+1]=token
    if native.defer_setup then native.pending_setup=token else callbacks.on_setup(true,token) end
  end
  function out.start(token,time,offset,s)assert(not token.dead and offset==0);token.started=true;native.start=time;native.play_stream=s end
  function out.stop(token)assert(not token.dead);native.stops=native.stops+1;token.started=false end
  function out.teardown(token)
    assert(not token.dead);token.dead=true;native.teardowns=native.teardowns+1
    token.callbacks.on_output_lost('synchronous teardown callback')
  end
  function out.get_local_time(token)assert(not token.dead);return native.now end
  function out.get_output_delay(token)assert(not token.dead);return native.delay end
  raat={output=out,source_selection={is_supported=false},
    session={message_handlers={},shutdown_handlers={},send_message=function(kind,b)assert(kind==2);replies[#replies+1]=b.bytes end},
    transport={is_supported=true,is_update_status_supported=true,is_update_artwork_supported=true,control_handlers={}}}
  function raat.transport.update_status(data)
    native.statuses[#native.statuses+1]=data
    if native.echo then for _,cb in ipairs(raat.transport.control_handlers)do cb('{"button":"playpause"}')end end
  end
  function raat.transport.update_artwork(mime,data)
    assert(mime==nil or (mime=='image/jpeg' and type(data)=='string'))
    native.artworks[#native.artworks+1]={mime=mime,data=data}
  end
  dofile(adapter)
end
local rid=0
local function rpc(op,args)
  rid=rid+1;local id=rid
  for _,cb in ipairs(raat.session.message_handlers)do cb(1,pack('>I4B',id,op)..(args or ''))end
  for i=#replies,1,-1 do
    local response,final,code=unpack('>I4BB',replies[i])
    if response==id then return code,replies[i]:sub(7),final end
  end
end
local function snap(data)return {unpack('>Bi8i8i8i8i8i8i8i8i8i8i8',data)}end
local audio_key=string.rep('\165',32)
local function connect(key)
  local listener=native.listener
  listener.accept_callback(nil)
  local connection=native.accepted
  if key then connection:deliver(key) end
  return connection
end
local function setup(bits,capacity,ready,no_connect)
  local code,data,final=rpc(2,pack('>BI4I2I2I4I4',bits==32 and 2 or 1,48000,bits,2,capacity,ready)..audio_key)
  if code==0 then
    check(#data==91 and unpack('>I2',data)==41016,'SETUP returns audio port followed by snapshot')
    if not no_connect then native.audio=connect(audio_key) end
    return code,data:sub(3),final
  end
  return code,data,final
end
local function packet(offset,frames,data)return pack('>Bi8I4',3,offset,frames)..data end
local function audio_pcm(offset,frames,data)native.audio:deliver(packet(offset,frames,data))end
local function response(id)
  for i=#replies,1,-1 do
    local response_id,final,code=unpack('>I4BB',replies[i])
    if response_id==id and final==1 then return code,replies[i]:sub(7) end
  end
end
local function tick()for _,t in ipairs(timers)do if t.callback then t.callback()end end end
local function close()for _,cb in ipairs(raat.session.shutdown_handlers)do cb()end end
reset();rpc(10)
local sent_after_shutdown=false
raat.session.send_message=function()sent_after_shutdown=true;error('native transport already closed')end
close()
check(not sent_after_shutdown,'session shutdown must not send on a closed native transport')

reset()
local code,data=rpc(1);local flags,count=unpack('>I4I4',data)
check(code==0 and count==3 and flags==31,'INFO must preserve supported native sample types and omit unsupported8-bit')
rpc(10)
check(rpc(12,'{"command":"status","owner_id":"jastreamer-\\u0061"}')==0,'escaped owner claim')
local count_before=#native.announcements
udp.callback(nil,' { "owner_id": null, "command": "query" } ')
check(#native.announcements==count_before+1,'native query must be answered without a Go roundtrip')
code,data=setup(24,8,4);check(code==0 and snap(data)[12]==1,'SETUP epoch: code='..tostring(code)..' data='..tostring(data))
local pcm='\1\2\3\4\5\6\7\8\9\10\11\12\13\14\15\16\17\18\19\20\21\22\23\24'
check(rpc(3,pack('>i8I4',0,4)..pcm)==1 and rpc(7)==1,'control PCM and EOF removed')
check(rpc(4)==nil and not native.start,'START waits asynchronously for native readiness')
local start_id=rid
audio_pcm(0,4,pcm)
check(response(start_id)==0 and native.start~=nil,'PCM readiness completes pending START')
check(native.stream.chunks[1].data==pcm,'24-bit samples unchanged with unity gain')
audio_pcm(4,4,pcm)
check(native.stream.stats.samples_in==8,'fill exact capacity without overwrite')
native.audio:deliver('\7')
native.stream.stats.samples_out=8;native.stream.stats.fill_samples=4
native.now=native.start+native.delay+166667
code,data=rpc(11);check(code==0 and snap(data)[1]==4 and snap(data)[2]==8,'completion requires consumed samples and device clock despite retained region')
check(native.stops==1 and native.teardowns==1,'completed stream stopped and released exactly once')
code,data=rpc(6);check(code==0 and snap(data)[12]==2,'STOP advances control epoch')
local before=#replies
for _,cb in ipairs(raat.transport.control_handlers)do cb('{"button":"play"}')end
tick()
local tagged=false
for i=before+1,#replies do local _,_,event=unpack('>I4BB',replies[i]);if event==4 then tagged=unpack('>i8',replies[i],7)==2 end end
check(tagged,'stopped device controls carry native emission epoch')
native.echo=true;before=#replies;check(rpc(8,'{"state":"playing"}')==0,'status publication');tick()
local echoed=false
for i=before+1,#replies do local _,_,event=unpack('>I4BB',replies[i]);if event==4 then echoed=true end end
check(not echoed,'synchronous publication echo is suppressed')
local jpeg='\255\216\0\1\0\2\255\217';check(rpc(9,jpeg)==0 and native.artworks[#native.artworks].data==jpeg,'binary artwork including NUL survives')
local status_count,art_count=#native.statuses,#native.artworks
udp.callback(nil,'{"command":"status","owner_id":"another-controller"}')
check(rpc(8,'{"state":"stopped"}')==7 and rpc(9,jpeg)==7,'ownership loss is nonfatal and immediately gates display publication')
close()
check(#native.statuses==status_count and #native.artworks==art_count,'shutdown must not clear new owner display')
check(timers[1].closed and udp.closed,'shutdown closes native handles')
reset();rpc(10);setup(16,8,4)
check(rpc(5)==0 and native.stops==0,'paused seek READY->PAUSED must not START or stop hardware')
code,data=rpc(11);check(snap(data)[1]==3,'paused state retained')
close()
reset();rpc(10);setup(16,8,4);audio_pcm(0,4,string.rep('\0',16));rpc(4)
native.stream.stats.samples_out=2;native.stream.stats.samples_dropped=1
code=rpc(11);check(code==1,'pre-EOF missing source frame fails')
code,data=rpc(11);check(code==0 and snap(data)[1]==6,'dropout is terminal loss, never natural completion')
close()
reset();rpc(10);setup(16,8,8);audio_pcm(0,4,string.rep('\0',16));native.audio:deliver('\7');rpc(4)
native.stream.stats.samples_out=8;native.stream.stats.samples_dropped=4;native.stream.stats.fill_samples=0
native.now=native.start+native.delay+83334
code,data=rpc(11);check(code==0 and snap(data)[1]==4,'only post-EOF zero-fill may complete')
close()
reset();rpc(10);native.defer_setup=true;setup(16,8,4);local stale=native.pending_setup
native.defer_setup=false;setup(24,8,4);local live=native.tokens[#native.tokens]
stale.callbacks.on_setup(true,stale)
check(stale.dead and not live.dead,'late setup releases its own token only')
stale.callbacks.on_output_lost('late loss');code,data=rpc(11)
check(code==0 and snap(data)[1]==1,'late output loss cannot invalidate replacement stream')
local epoch=snap(data)[12];check(rpc(2,'bad')==1,'malformed SETUP rejected');code,data=rpc(11)
check(snap(data)[12]==epoch,'malformed SETUP cannot change current epoch')
close()

reset();setup(16,8,4,true)
local listener=native.listener
local candidate=connect()
candidate:deliver(audio_key:sub(1,11))
check(native.stream.stats.samples_in==0 and not listener.closed,'partial token cannot admit PCM')
local rejected=connect()
check(rejected.closed and not candidate.closed,'one bounded candidate cannot be displaced')
candidate:deliver(string.rep('\0',21))
check(candidate.closed and not listener.closed,'wrong token rejected without losing stream')
native.audio=connect()
local payload=packet(0,4,string.rep('\127',16))
native.audio:deliver(audio_key:sub(1,19))
native.audio:deliver(audio_key:sub(20)..payload:sub(1,5))
check(listener.closed and native.stream.stats.samples_in==0,'authenticated fragmented packet remains pending')
native.audio:deliver(payload:sub(6,15))
check(native.stream.stats.samples_in==0,'partial PCM never reaches unsafe native write')
native.audio:deliver(payload:sub(16))
check(native.stream.stats.samples_in==4 and native.stream.chunks[1].data==string.rep('\127',16),'fragmented authentication and PCM reconstruct exact bytes')
close()

reset();setup(16,8,4,true)
candidate=connect(audio_key:sub(1,1))
for _=1,250 do tick()end
check(candidate.closed and not native.listener.closed,'unauthenticated candidate expires without losing stream')
native.audio=connect(audio_key)
check(not native.audio.closed,'new candidate can authenticate after timeout')
close()

reset();setup(16,8,4)
local connection=native.audio
local before_pcm=#replies
connection:deliver(packet(0,4,string.rep('\1',16))..packet(4,4,string.rep('\2',16))..packet(8,4,string.rep('\3',16))..'\7')
check(#replies==before_pcm,'audio packets produce no per-packet control ACK')
check(native.stream.stats.samples_in==8 and not connection.reading,'full ring pauses reads before any overwrite')
check(rpc(4)==0,'buffered readiness starts hardware while reads paused')
native.stream.stats.samples_out=4;native.stream.stats.fill_samples=4
tick()
check(native.stream.stats.samples_in==12 and connection.reading and connection.read_starts==2,'timer drains pending PCM and resumes native reads')
check(native.stream.chunks[3].offset==8 and native.stream.chunks[3].data==string.rep('\3',16),'backpressured packet retains offset and bytes')
connection:deliver(nil)
code,data=rpc(11);check(snap(data)[1]==2 and connection.closed,'TCP EOF after marker does not itself complete playback')
native.stream.stats.samples_out=12;native.stream.stats.fill_samples=0
native.now=native.start+native.delay+250000
code,data=rpc(11);check(snap(data)[1]==4,'buffered EOF preserves consumed-frame completion')
close()

reset();setup(16,20000,16384)
local max_pcm=string.rep('\171',65536)
local max_packet=packet(0,16384,max_pcm)
native.audio:deliver(max_packet:sub(1,65536))
check(native.stream.stats.samples_in==0,'maximum packet header fragmentation does not shorten payload')
native.audio:deliver(max_packet:sub(65537))
check(native.stream.chunks[1].data==max_pcm,'maximum legal PCM payload arrives unchanged')
close()

local invalid_audio={
  {name='unknown opcode',data='\4'},
  {name='zero frames',data=packet(0,0,'')},
  {name='source gap',data=packet(1,1,string.rep('\0',4))},
  {name='negative offset',data=packet(-1,1,string.rep('\0',4))},
  {name='oversized payload declaration',data=packet(0,16385,'')},
  {name='packet larger than ring',data=packet(0,9,'')},
  {name='empty source EOF',data='\7'},
  {name='bounded receive overflow',data=string.rep('\0',2*65536+65)},
  {name='duplicate packet',data=packet(0,1,string.rep('\0',4))..packet(0,1,string.rep('\0',4))},
  {name='trailing bytes after EOF',data=packet(0,1,string.rep('\0',4))..'\7\3'},
}
for _,invalid in ipairs(invalid_audio)do
  reset();setup(16,8,4);check(rpc(4)==nil,'pending START before '..invalid.name)
  local waiting=rid
  native.audio:deliver(invalid.data)
  code,data=rpc(11)
  check(snap(data)[1]==6 and native.audio.closed and response(waiting)==1,invalid.name..' fails closed and cancels START')
  close()
end

for _,partial in ipairs({'', '\3',packet(0,4,string.rep('\0',15))})do
  reset();setup(16,8,4)
  if #partial>0 then native.audio:deliver(partial)end
  check(native.stream.stats.samples_in==0,'incomplete payload is not written')
  native.audio:deliver(nil)
  code,data=rpc(11);check(snap(data)[1]==6,'TCP EOF before marker is terminal, including truncated payload')
  close()
end

reset();setup(16,8,8);check(rpc(4)==nil,'short source START initially pending')
start_id=rid
audio_pcm(0,2,string.rep('\0',8))
check(response(start_id)==nil and not native.start,'below native readiness remains asynchronous')
native.audio:deliver('\7')
check(response(start_id)==0 and native.start~=nil,'short nonempty EOF releases pending START')
close()

for _,action in ipairs({'pause','stop','replace','shutdown','loss'})do
  reset();setup(16,8,4);check(rpc(4)==nil,'pending START for '..action)
  local waiting,old_connection,old_token=rid,native.audio,native.tokens[1]
  check(rpc(4)==1 and response(waiting)==nil,'duplicate START cannot replace the pending request')
  local stale_read=old_connection.callback
  if action=='pause' then rpc(5)
  elseif action=='stop' then rpc(6)
  elseif action=='replace' then setup(24,8,4)
  elseif action=='shutdown' then close()
  else old_token.callbacks.on_output_lost('native device lost')end
  check(old_connection.closed,'lifecycle '..action..' closes audio')
  if action=='shutdown' then
    check(response(waiting)==nil,'disconnected transport cannot acknowledge pending START')
  else check(response(waiting)==1,'lifecycle '..action..' cancels START on the live transport')end
  if action=='pause' then
    check(not old_token.dead and native.stops==0,'pause retains prepared native token without starting')
  else check(old_token.dead,'lifecycle '..action..' tears down native token')end
  local live_stream=native.stream
  stale_read(nil,packet(0,1,string.rep('\0',4)))
  check(live_stream.stats.samples_in==0,'stale read after '..action..' cannot write')
  close()
end

reset();native.bind_error=true
check(setup(16,8,4)==1 and native.tokens[1].dead and native.sockets[1].closed,'listener bind failure releases native token and socket')
close()

reset();setup(16,8,4);rpc(4);start_id=rid
function native.stream:write()error('native write exploded')end
native.audio:deliver(packet(0,4,string.rep('\0',16)))
code,data=rpc(11)
check(snap(data)[1]==6 and response(start_id)==1,'read callback contains native write exceptions')
close()

reset();setup(16,8,4)
function native.stream:get_stats()error('native stats exploded')end
tick()
code,data=rpc(11)
check(snap(data)[1]==6 and native.audio.closed,'timer callback contains native counter exceptions')
close()

reset();setup(16,8,4);rpc(4)
raat.session.send_message=function()error('control send exploded')end
native.close_error=true
native.audio:deliver(packet(0,4,string.rep('\0',16)))
check(native.audio.closed and native.tokens[1].dead and timers[1].closed,'callback contains failed replies and cleanup exceptions')
close()

reset();setup(16,8,4,true);native.accept_error=true
native.listener.accept_callback(nil)
code,data=rpc(11)
check(snap(data)[1]==6 and native.accepted.closed and native.listener.closed,'accept callback contains exceptions and closes unaccepted handle')
close()

reset();setup(16,8,4,true)
candidate=connect()
candidate:deliver(nil,'ECONNRESET')
code,data=rpc(11)
check(snap(data)[1]==1 and candidate.closed and not native.listener.closed,'unauthenticated socket error cannot steal prepared stream')
native.audio=connect(audio_key)
native.audio:deliver(nil,'ECONNRESET')
code,data=rpc(11)
check(snap(data)[1]==6 and native.audio.closed,'authenticated socket error is terminal loss')
close()
for _,socket in ipairs(native.sockets)do check(socket.close_count==1,'TCP handle closes exactly once')end

reset();setup(16,8,4,true)
listener=native.listener
candidate=connect(audio_key:sub(1,7))
rpc(5)
check(listener.closed and candidate.closed and not native.tokens[1].dead,'pause closes listener and unauthenticated candidate while retaining native token')
close()

print('Authored receiver Lua smoke: '..checks..' behavioral checks passed; native APIs simulated, no devices contacted.')
return checks
