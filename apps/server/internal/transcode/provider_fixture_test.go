//go:build !windows

package transcode

import "testing"

func availableApproval(t *testing.T, path string) *Approval {
	t.Helper()
	executable, err := bindExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	approval := newApproval(Diagnostic{Status: StatusAvailable, SHA256: executable.fingerprint}, executable)
	t.Cleanup(func() {
		if err := approval.Close(); err != nil {
			t.Error(err)
		}
	})
	return approval
}

func newTestProvider(t *testing.T, config Config) *Provider {
	t.Helper()
	provider, err := NewProvider(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	return provider
}

const fakeProgramStream = `#!/bin/sh
expected='-nostdin -hide_banner -loglevel error -i pipe:0 -vn -sn -dn -ac 2 -ar 44100 -acodec pcm_s16be -f s16be pipe:1'
[ "$*" = "$expected" ] || { printf 'bad args' >&2; exit 64; }
cat >/dev/null
printf '\022\064\376\334'
`

const fakeProgramStderrFailure = `#!/bin/sh
i=0
while [ "$i" -lt 20000 ]; do printf 'private diagnostic flood private diagnostic flood\n' >&2; i=$((i+1)); done
exit 9
`

const fakeProgramTree = `#!/bin/sh
( while :; do :; done ) &
printf '%s' "$!" >"$PIDFILE"
printf '\001'
wait
`

const fakeProgramBlocking = `#!/bin/sh
printf 'launch\n' >>"$LAUNCHES"
printf '\001'
while :; do :; done
`

const fakeProgramSeek = `#!/bin/sh
expected='-nostdin -hide_banner -loglevel error -i pipe:0 -ss 1.250000 -vn -sn -dn -ac 2 -ar 44100 -acodec pcm_s16be -f s16be pipe:1'
[ "$*" = "$expected" ] || { printf 'bad args: %s' "$*" >&2; exit 64; }
source=$(cat)
[ "$source" = 'encoded' ] || { printf 'source did not begin at codec boundary' >&2; exit 65; }
printf 'seeked'
`

const fakeProgramFastSuccess = `#!/bin/sh
printf 'ok'
`
