import 'package:jstreamer_control/behavior_model.dart';
import 'package:jstreamer_control/control_models.dart';

ControlState applyPolicyView(ControlState state, PolicyView policyView) {
  final policy = policyView.mode.known;
  if (policy == null) return state;
  var nextState = state
      .markPolicySaved(
        policy: policy,
        serverRevision: policyView.revision,
      )
      .reduce(SetArtistCooldown(policyView.artistGap))
      .reduce(SetAlbumCooldown(policyView.albumGap));
  final sessionOverride = policyView.sessionOverride;
  if (sessionOverride == null) {
    return nextState.reduce(const SelectSessionOverride(null));
  }
  if (sessionOverride.known case final override?) {
    nextState = nextState.reduce(SelectSessionOverride(override));
  }
  return nextState;
}
