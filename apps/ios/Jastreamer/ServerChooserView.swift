import SwiftUI

struct ServerChooserView: View {
    @ObservedObject var model: ClientModel
    @ObservedObject var recentServers: RecentServers
    @ObservedObject var discovery: ServerDiscovery
    let language: String

    @State private var address = ""
    @FocusState private var addressFocused: Bool

    var body: some View {
        let text = L10n(language)
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(text("select.title"))
                        .font(.largeTitle.bold())
                    Text(text("select.detail"))
                        .foregroundStyle(.secondary)
                }

                VStack(alignment: .leading, spacing: 8) {
                    Text(text("server.address"))
                        .font(.headline)
                    TextField("music-server.local:8080", text: $address)
                        .textContentType(.URL)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.go)
                        .focused($addressFocused)
                        .frame(minHeight: 44)
                        .accessibilityIdentifier("server-address")
                        .onSubmit {
                            connectManual()
                        }
                    Button {
                        connectManual()
                    } label: {
                        Label(text("verify.connect"), systemImage: "checkmark.shield")
                            .frame(maxWidth: .infinity, minHeight: 44)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.isConnecting || address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("connect-server")
                    Text(text("http.notice"))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .padding()
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 16))

                if model.isConnecting {
                    HStack(spacing: 12) {
                        ProgressView()
                        Text(text("connecting"))
                        Spacer()
                        Button(text("cancel"), role: .cancel) {
                            model.cancelConnection()
                        }
                        .frame(minWidth: 44, minHeight: 44)
                        .accessibilityIdentifier("cancel-connection")
                    }
                    .padding(.horizontal, 4)
                    .accessibilityElement(children: .contain)
                } else if model.selectionError != nil {
                    Button {
                        addressFocused = false
                        model.retrySelection()
                    } label: {
                        Label(text("retry.connection"), systemImage: "arrow.clockwise")
                            .frame(minHeight: 44)
                    }
                    .accessibilityIdentifier("retry-connection")
                }

                ServerSectionHeader(title: text("nearby.servers"), language: language) {
                    model.refreshDiscovery()
                }

                if discovery.servers.isEmpty {
                    HStack(alignment: .top, spacing: 10) {
                        ProgressView()
                        Text(discovery.error == nil ? text("nearby.empty") : text("discovery.error"))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.vertical, 8)
                } else {
                    LazyVStack(spacing: 10) {
                        ForEach(discovery.servers, id: \.self) { server in
                            ServerRow(server: server, language: language, verified: true) {
                                addressFocused = false
                                model.connect(server.origin, expectedID: server.id)
                            }
                            .accessibilityIdentifier("nearby-server-\(server.profileID.uuidString)")
                        }
                    }
                }

                Text(text("recent.servers"))
                    .font(.title2.bold())
                    .padding(.top, 4)

                if recentServers.servers.isEmpty {
                    Text(text("recent.empty"))
                        .foregroundStyle(.secondary)
                        .padding(.vertical, 8)
                } else {
                    LazyVStack(spacing: 10) {
                        ForEach(recentServers.servers, id: \.self) { server in
                            HStack(spacing: 8) {
                                ServerRow(server: server, language: language, verified: false) {
                                    addressFocused = false
                                    model.connect(server.origin, expectedID: server.id)
                                }
                                Button(role: .destructive) {
                                    model.removeRecent(server)
                                } label: {
                                    Image(systemName: "trash")
                                        .frame(width: 44, height: 44)
                                }
                                .accessibilityLabel("\(text("remove.recent")): \(server.name)")
                                .accessibilityIdentifier("remove-recent-\(server.profileID.uuidString)")
                            }
                        }
                    }
                }
            }
            .frame(maxWidth: 760, alignment: .leading)
            .padding(20)
        }
        .scrollDismissesKeyboard(.interactively)
    }

    private func connectManual() {
        addressFocused = false
        model.connect(address)
    }
}

private struct ServerSectionHeader: View {
    let title: String
    let language: String
    let refresh: () -> Void

    var body: some View {
        let text = L10n(language)
        HStack {
            Text(title)
                .font(.title2.bold())
            Spacer()
            Button(action: refresh) {
                Image(systemName: "arrow.clockwise")
                    .frame(width: 44, height: 44)
            }
            .accessibilityLabel(text("refresh.discovery"))
            .accessibilityIdentifier("refresh-discovery")
        }
    }
}

private struct ServerRow: View {
    let server: ServerEndpoint
    let language: String
    let verified: Bool
    let action: () -> Void

    var body: some View {
        let text = L10n(language)
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: verified ? "checkmark.seal.fill" : "clock.arrow.circlepath")
                    .font(.title2)
                    .foregroundStyle(verified ? Color.green : Color(uiColor: .secondaryLabel))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 3) {
                    Text(server.name)
                        .font(.headline)
                        .lineLimit(1)
                    Text(server.origin)
                        .font(.subheadline.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Text("\(verified ? text("verified") : text("verify.on.connect")) · \(server.version)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 4)
                Image(systemName: "chevron.right")
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .contentShape(Rectangle())
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 14))
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(server.name), \(server.origin), \(verified ? text("verified") : text("verify.on.connect"))")
    }
}
