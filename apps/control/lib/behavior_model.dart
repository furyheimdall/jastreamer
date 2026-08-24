export 'package:jastreamer_control/decision_reason.dart';

enum Policy { stop, album, similar }

enum PairingStatus { available, pairing, paired, failed }

enum PreviewCommitment { revocable, committed }

extension PolicyLabel on Policy {
  String get korean => switch (this) {
        Policy.stop => '재생 종료',
        Policy.album => '앨범 이어듣기',
        Policy.similar => '비슷한 음악',
      };
}

extension type const ServerId(String value) {}

final class DiscoveredServer {
  const DiscoveredServer({
    required this.id,
    required this.name,
    required this.origin,
    required this.pairingUrl,
    required this.certificateSha256,
    required this.pairing,
    this.token,
  });
  final ServerId id;
  final String name;
  final Uri origin;
  final Uri pairingUrl;
  final String certificateSha256;
  final PairingStatus pairing;
  final String? token;

  DiscoveredServer withPairing(PairingStatus value) => DiscoveredServer(
        id: id,
        name: name,
        origin: origin,
        pairingUrl: pairingUrl,
        certificateSha256: certificateSha256,
        pairing: value,
      );
}

sealed class ControlAction {
  const ControlAction();
}

final class DiscoverServers extends ControlAction {
  const DiscoverServers();
}

final class ServerDiscovered extends ControlAction {
  const ServerDiscovered(this.server);
  final DiscoveredServer server;
}

final class OpenPairing extends ControlAction {
  const OpenPairing(this.serverId);
  final ServerId serverId;
}

final class PairingReturned extends ControlAction {
  const PairingReturned(this.serverId);
  final ServerId serverId;
}

final class SelectPolicy extends ControlAction {
  const SelectPolicy(this.policy);
  final Policy policy;
}

final class SelectSessionOverride extends ControlAction {
  const SelectSessionOverride(this.policy);
  final Policy? policy;
}

final class SetArtistCooldown extends ControlAction {
  const SetArtistCooldown(this.value);
  final int value;
}

final class SetAlbumCooldown extends ControlAction {
  const SetAlbumCooldown(this.value);
  final int value;
}

final class ResetCooldowns extends ControlAction {
  const ResetCooldowns();
}

final class PolicySaveRejected extends ControlAction {
  const PolicySaveRejected({required this.serverRevision});
  final int serverRevision;
}

final class ServerPolicyRefreshed extends ControlAction {
  const ServerPolicyRefreshed({required this.policy, required this.revision});
  final Policy policy;
  final int revision;
}

final class ControlState {
  static const defaultArtistCooldown = 4;
  static const defaultAlbumCooldown = 10;

  const ControlState({
    required this.discovered,
    required this.servers,
    required this.persistedPolicy,
    required this.pendingPolicy,
    required this.sessionOverride,
    required this.artistCooldown,
    required this.albumCooldown,
    required this.policyRevision,
    required this.serverPolicyChanged,
  });

  static final fixture = ControlState(
    discovered: false,
    servers: <DiscoveredServer>[
      DiscoveredServer(
        id: const ServerId('living-room'),
        name: 'Living room Server',
        origin: Uri.parse('https://living-room.local:8443'),
        pairingUrl: Uri.parse('https://living-room.local:8443/pair/'),
        certificateSha256: '9A:71:4C:20:83:EE:1B:56:4D:8A:11:90:71:22:CE:44',
        pairing: PairingStatus.available,
      ),
    ],
    persistedPolicy: Policy.stop,
    pendingPolicy: Policy.stop,
    sessionOverride: null,
    artistCooldown: defaultArtistCooldown,
    albumCooldown: defaultAlbumCooldown,
    policyRevision: 7,
    serverPolicyChanged: false,
  );

  final bool discovered;
  final List<DiscoveredServer> servers;
  final Policy persistedPolicy;
  final Policy pendingPolicy;
  final Policy? sessionOverride;
  final int artistCooldown;
  final int albumCooldown;
  final int policyRevision;
  final bool serverPolicyChanged;
  Policy get effectivePolicy => persistedPolicy;
  Policy get desiredPolicy => pendingPolicy;
  bool get hasStaleIntent =>
      serverPolicyChanged && pendingPolicy != persistedPolicy;
  bool get isPolicySaved =>
      !serverPolicyChanged && pendingPolicy == persistedPolicy;

  ControlState markPolicySaved({
    required Policy policy,
    required int serverRevision,
  }) =>
      _copy(
        persistedPolicy: policy,
        pendingPolicy: policy,
        policyRevision: serverRevision,
        serverPolicyChanged: false,
      );

  ControlState reduce(ControlAction action) => switch (action) {
        DiscoverServers() => _copy(discovered: true),
        ServerDiscovered(:final server) => _copy(
            discovered: true,
            servers: <DiscoveredServer>[server],
          ),
        OpenPairing(:final serverId) => _pair(serverId, PairingStatus.pairing),
        PairingReturned(:final serverId) =>
          _pair(serverId, PairingStatus.paired),
        SelectPolicy(:final policy) => _copy(pendingPolicy: policy),
        SelectSessionOverride(:final policy) => _copy(
            sessionOverride: policy,
            clearSessionOverride: policy == null,
          ),
        SetArtistCooldown(:final value) => _copy(artistCooldown: value),
        SetAlbumCooldown(:final value) => _copy(albumCooldown: value),
        ResetCooldowns() => _copy(
            artistCooldown: defaultArtistCooldown,
            albumCooldown: defaultAlbumCooldown,
          ),
        PolicySaveRejected(:final serverRevision) => _copy(
            policyRevision: serverRevision,
            serverPolicyChanged: true,
          ),
        ServerPolicyRefreshed(:final policy, :final revision) => _copy(
            persistedPolicy: policy,
            policyRevision: revision,
            serverPolicyChanged: true,
          ),
      };

  ControlState _pair(ServerId id, PairingStatus pairing) => _copy(
        servers: servers
            .map((server) =>
                server.id == id ? server.withPairing(pairing) : server)
            .toList(growable: false),
      );

  ControlState _copy({
    bool? discovered,
    List<DiscoveredServer>? servers,
    Policy? persistedPolicy,
    Policy? pendingPolicy,
    Policy? sessionOverride,
    bool clearSessionOverride = false,
    int? artistCooldown,
    int? albumCooldown,
    int? policyRevision,
    bool? serverPolicyChanged,
  }) =>
      ControlState(
        discovered: discovered ?? this.discovered,
        servers: servers ?? this.servers,
        persistedPolicy: persistedPolicy ?? this.persistedPolicy,
        pendingPolicy: pendingPolicy ?? this.pendingPolicy,
        sessionOverride: clearSessionOverride
            ? null
            : sessionOverride ?? this.sessionOverride,
        artistCooldown: artistCooldown ?? this.artistCooldown,
        albumCooldown: albumCooldown ?? this.albumCooldown,
        policyRevision: policyRevision ?? this.policyRevision,
        serverPolicyChanged: serverPolicyChanged ?? this.serverPolicyChanged,
      );
}
