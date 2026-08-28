use jastreamer_renderer::endpoint::{
    EndpointEvent, EndpointPolicy, EndpointPreference, EndpointTransition,
};

#[test]
fn default_endpoint_change_requests_recovery_for_default_selection() {
    // Given
    let mut policy = EndpointPolicy::new(EndpointPreference::Default);

    // When
    let transition = policy.transition(EndpointEvent::DefaultChanged, true);

    // Then
    assert_eq!(transition, EndpointTransition::Recover);
}

#[test]
fn default_endpoint_change_preserves_named_selection() {
    // Given
    let mut policy = EndpointPolicy::new(EndpointPreference::Named("Speakers".to_owned()));

    // When
    let transition = policy.transition(EndpointEvent::DefaultChanged, true);

    // Then
    assert_eq!(transition, EndpointTransition::KeepCurrent);
}

#[test]
fn removed_named_endpoint_requests_unavailable_transition() {
    // Given
    let mut policy = EndpointPolicy::new(EndpointPreference::Named("Speakers".to_owned()));

    // When
    let transition = policy.transition(EndpointEvent::TopologyChanged, false);

    // Then
    assert_eq!(transition, EndpointTransition::Unavailable);
}

#[test]
fn stream_invalidation_always_requests_recovery() {
    // Given
    let mut policy = EndpointPolicy::new(EndpointPreference::Named("Speakers".to_owned()));

    // When
    let transition = policy.transition(EndpointEvent::StreamInvalidated, true);

    // Then
    assert_eq!(transition, EndpointTransition::Recover);
}

#[test]
fn restored_named_endpoint_requests_recovery_after_unavailable_transition() {
    // Given
    let mut policy = EndpointPolicy::new(EndpointPreference::Named("Speakers".to_owned()));
    assert_eq!(
        policy.transition(EndpointEvent::TopologyChanged, false),
        EndpointTransition::Unavailable,
    );

    // When
    let transition = policy.transition(EndpointEvent::TopologyChanged, true);

    // Then
    assert_eq!(transition, EndpointTransition::Recover);
}
