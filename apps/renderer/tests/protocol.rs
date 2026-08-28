use jastreamer_renderer::{
    FakeBackend, Negotiation, ProtocolError, Renderer, SUPPORTED_MAJORS, capabilities_for_major,
    parse_renderer_command,
};
#[test]
fn protocol_v3_and_v2_negotiate() {
    assert_eq!(SUPPORTED_MAJORS, [3, 2]);
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(Negotiation {
            local_majors: &SUPPORTED_MAJORS,
            remote_majors: &[3, 2],
            required_capabilities: &[],
            remote_capabilities: &[],
        })
        .expect("v3"),
        3
    );
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(Negotiation {
            local_majors: &SUPPORTED_MAJORS,
            remote_majors: &[2],
            required_capabilities: &[],
            remote_capabilities: &[],
        })
        .expect("v2"),
        2
    );
}
#[test]
fn negotiation_chooses_highest_common_major_and_capability() {
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(Negotiation {
            local_majors: &[3, 2],
            remote_majors: &[2, 3],
            required_capabilities: &["render"],
            remote_capabilities: &["render", "future"],
        }),
        Ok(3)
    );
}

#[test]
fn v2_capabilities_do_not_claim_v3_features() {
    assert_eq!(capabilities_for_major(2), &["render"]);
    assert!(!capabilities_for_major(2).contains(&"renderer-session"));
}

#[test]
fn unknown_renderer_command_is_typed_without_side_effect() {
    assert_eq!(
        parse_renderer_command(3, "command-future", "future-command"),
        Err(ProtocolError::UnsupportedCommand {
            command_id: "command-future".to_owned(),
            kind: "future-command".to_owned(),
        })
    );
}

#[test]
fn v2_rejects_v3_only_renderer_command() {
    assert!(matches!(
        parse_renderer_command(2, "command-pause", "pause"),
        Err(ProtocolError::UnsupportedCommand { .. })
    ));
}

#[test]
fn missing_capability_is_typed() {
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(Negotiation {
            local_majors: &[3, 2],
            remote_majors: &[2, 3],
            required_capabilities: &["render"],
            remote_capabilities: &[],
        }),
        Err(ProtocolError::MissingCapability {
            capability: "render".to_owned()
        })
    );
}

#[test]
fn unsupported_major_is_typed_and_non_panicking() {
    assert_eq!(
        Renderer::<FakeBackend>::negotiate(Negotiation {
            local_majors: &[3, 2],
            remote_majors: &[99],
            required_capabilities: &[],
            remote_capabilities: &[],
        }),
        Err(ProtocolError::UnsupportedProtocolMajor { requested: 99 })
    );
}
#[test]
fn fake_backend_reports_diagnostics() {
    let mut renderer = Renderer::new(FakeBackend::default());
    renderer.start().expect("fake backend starts");
    assert!(renderer.diagnostics().contains("fake"));
}
