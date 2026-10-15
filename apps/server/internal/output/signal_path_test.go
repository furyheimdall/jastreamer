package output

import "testing"

type signalMedia struct {
	fakeMedia
	calls int
	path  SignalPath
}

func (media *signalMedia) SignalPath(string, string) (SignalPath, bool) {
	media.calls++
	return media.path, true
}

func TestSignalPathRejectsAmbiguousOfflineAndForeignBackendEvidence(t *testing.T) {
	for _, boundary := range []string{"ambiguous", "offline", "unknown", "missing play", "foreign renderer", "unsupported provider"} {
		t.Run(boundary, func(t *testing.T) {
			controller := &fakeController{device: Device{ID: "renderer", Protocol: ProtocolUPnP, Online: true}}
			media := &signalMedia{path: SignalPath{Scope: "server", Mode: "original", RendererID: "renderer", TrackID: "track"}}
			manager := NewManager(Backend{Protocol: ProtocolUPnP, Controller: controller, Media: media})
			rendererID, playID := "renderer", "play"
			switch boundary {
			case "ambiguous":
				manager.backends = append(manager.backends, Backend{Protocol: ProtocolCast, Controller: &fakeController{device: Device{ID: "renderer", Protocol: ProtocolCast, Online: true}}, Media: media})
			case "offline":
				controller.device.Online = false
			case "unknown":
				rendererID = "unknown"
			case "missing play":
				playID = ""
			case "foreign renderer":
				media.path.RendererID = "foreign"
			case "unsupported provider":
				manager.backends[0].Media = &fakeMedia{}
			}
			if path, ok := manager.SignalPath(rendererID, playID); ok {
				t.Fatalf("unsafe backend evidence escaped: %+v", path)
			}
			if boundary != "foreign renderer" && media.calls != 0 {
				t.Fatal("unowned or unusable output consulted its provider")
			}
			if len(controller.calls) != 0 {
				t.Fatal("signal-path inspection invoked renderer control")
			}
		})
	}
}
