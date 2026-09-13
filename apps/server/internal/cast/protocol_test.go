package cast

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestReadCastMessageRejectsOversizedFrameBeforeBodyRead(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], maxFrameSize+1)
	if _, err := readCastMessage(bytes.NewReader(header[:])); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("oversized frame error = %v, want ErrInvalidResponse", err)
	}
}

func TestUnmarshalCastMessageRejectsTruncatedLengthDelimitedField(t *testing.T) {
	valid, err := marshalCastMessage(castMessage{
		source: "receiver-0", destination: "sender-test",
		namespace: heartbeatNamespace, payload: []byte(`{"type":"PING"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	valid = valid[:len(valid)-1]
	if _, err := unmarshalCastMessage(valid); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("truncated envelope error = %v, want ErrInvalidResponse", err)
	}
}
