#[derive(Clone, Debug, PartialEq, Eq)]
pub enum EndpointPreference {
    Default,
    Named(String),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum EndpointEvent {
    DefaultChanged,
    TopologyChanged,
    StreamInvalidated,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum EndpointTransition {
    KeepCurrent,
    Recover,
    Unavailable,
}

#[derive(Clone, Debug)]
pub struct EndpointPolicy {
    preference: EndpointPreference,
    available: bool,
}

impl EndpointPolicy {
    pub const fn new(preference: EndpointPreference) -> Self {
        Self {
            preference,
            available: true,
        }
    }

    pub fn preference(&self) -> &EndpointPreference {
        &self.preference
    }

    pub fn transition(
        &mut self,
        event: EndpointEvent,
        selected_endpoint_present: bool,
    ) -> EndpointTransition {
        let was_available = self.available;
        self.available = selected_endpoint_present;
        match (
            &self.preference,
            event,
            selected_endpoint_present,
            was_available,
        ) {
            (_, EndpointEvent::StreamInvalidated, true, _) => EndpointTransition::Recover,
            (_, EndpointEvent::StreamInvalidated, false, _) => EndpointTransition::Unavailable,
            (EndpointPreference::Default, EndpointEvent::DefaultChanged, true, _) => {
                EndpointTransition::Recover
            }
            (EndpointPreference::Default, EndpointEvent::DefaultChanged, false, _) => {
                EndpointTransition::Unavailable
            }
            (EndpointPreference::Named(_), EndpointEvent::DefaultChanged, _, _) => {
                EndpointTransition::KeepCurrent
            }
            (_, EndpointEvent::TopologyChanged, true, false) => EndpointTransition::Recover,
            (_, EndpointEvent::TopologyChanged, true, true) => EndpointTransition::KeepCurrent,
            (_, EndpointEvent::TopologyChanged, false, _) => EndpointTransition::Unavailable,
        }
    }
}
