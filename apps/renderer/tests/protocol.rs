use jstreamer_renderer::{FakeBackend, ProtocolError, Renderer, SUPPORTED_MAJORS};
#[test]
fn protocol_n_and_n_minus_one_negotiate() {
    assert_eq!(SUPPORTED_MAJORS, [2, 1]);
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(&[1, 2], &[1, 2], &[], &[]).expect("v2"),
        2
    );
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(&[1, 2], &[1], &[], &[]).expect("v1"),
        1
    );
}
#[test]
fn negotiation_chooses_highest_common_major_and_capability() {
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(&[1, 2], &[1, 2], &["render"], &["render", "future"]),
        Ok(2)
    );
}

#[test]
fn missing_capability_is_typed() {
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(&[1, 2], &[1, 2], &["render"], &[]),
        Err(ProtocolError::MissingCapability {
            capability: "render".to_owned()
        })
    );
}

#[test]
fn unsupported_major_is_typed_and_non_panicking() {
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(&[1, 2], &[99], &[], &[]),
        Err(ProtocolError::UnsupportedProtocolMajor { requested: 99 })
    );
}
#[test]
fn fake_backend_reports_diagnostics() {
    let mut renderer = Renderer::new(FakeBackend::default());
    renderer.start().expect("fake backend starts");
    assert!(renderer.diagnostics().contains("fake"));
}
