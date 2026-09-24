import SwiftUI

struct ServerChooserView: View {
    @ObservedObject var model: ClientModel
    @ObservedObject var recentServers: RecentServers
    @ObservedObject var discovery: ServerDiscovery
    let language: String

    @State private var address = ""
    @State private var showsManualConnection = false

    var body: some View {
        let text = L10n(language)
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                HStack(alignment: .top, spacing: 14) {
                    Image(systemName: "server.rack")
                        .font(.title2.weight(.semibold))
                        .foregroundStyle(Color.accentColor)
                        .frame(width: 46, height: 46)
                        .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 14))
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 6) {
                        Text(text("select.title"))
                            .font(.largeTitle.bold())
                        Text(text("select.detail"))
                            .foregroundStyle(.secondary)
                    }
                }

                Button {
                    showsManualConnection = true
                } label: {
                    HStack(spacing: 14) {
                        Image(systemName: "keyboard")
                            .font(.title3.weight(.semibold))
                            .foregroundStyle(Color.accentColor)
                            .frame(width: 42, height: 42)
                            .background(Color.accentColor.opacity(0.10), in: RoundedRectangle(cornerRadius: 12))
                        VStack(alignment: .leading, spacing: 3) {
                            Text(text("connect.by.address"))
                                .font(.headline)
                            Text(text("connect.by.address.detail"))
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 4)
                        Image(systemName: "chevron.right")
                            .foregroundStyle(.tertiary)
                            .accessibilityHidden(true)
                    }
                    .contentShape(Rectangle())
                    .padding(14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 16))
                    .overlay {
                        RoundedRectangle(cornerRadius: 16)
                            .stroke(Color.primary.opacity(0.06))
                    }
                }
                .buttonStyle(.plain)
                .disabled(model.isConnecting)
                .accessibilityIdentifier("connect-by-address")

                if !showsManualConnection {
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
                            model.retrySelection()
                        } label: {
                            Label(text("retry.connection"), systemImage: "arrow.clockwise")
                                .frame(minHeight: 44)
                        }
                        .accessibilityIdentifier("retry-connection")
                    }
                }

                ChooserSectionCard {
                    ServerSectionHeader(title: text("nearby.servers"), language: language) {
                        model.refreshDiscovery()
                    }

                    Divider()

                    if discovery.servers.isEmpty {
                        HStack(alignment: .top, spacing: 10) {
                            ProgressView()
                            Text(discovery.error == nil ? text("nearby.empty") : text("discovery.error"))
                                .foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 6)
                    } else {
                        LazyVStack(spacing: 10) {
                            ForEach(discovery.servers, id: \.self) { server in
                                ServerRow(server: server, language: language, verified: true) {
                                    model.connect(server.origin, expectedID: server.id)
                                }
                                .accessibilityIdentifier("nearby-server-\(server.profileID.uuidString)")
                            }
                        }
                    }
                }

                ChooserSectionCard {
                    Text(text("recent.servers"))
                        .font(.title2.bold())

                    Divider()

                    if recentServers.servers.isEmpty {
                        Text(text("recent.empty"))
                            .foregroundStyle(.secondary)
                            .padding(.vertical, 6)
                    } else {
                        LazyVStack(spacing: 10) {
                            ForEach(recentServers.servers, id: \.self) { server in
                                HStack(spacing: 8) {
                                    ServerRow(server: server, language: language, verified: false) {
                                        model.connect(server.origin, expectedID: server.id)
                                    }
                                    Button(role: .destructive) {
                                        model.removeRecent(server)
                                    } label: {
                                        Image(systemName: "trash")
                                            .frame(width: 44, height: 44)
                                            .background(
                                                Color(uiColor: .systemBackground),
                                                in: RoundedRectangle(cornerRadius: 12)
                                            )
                                    }
                                    .accessibilityLabel("\(text("remove.recent")): \(server.name)")
                                    .accessibilityIdentifier("remove-recent-\(server.profileID.uuidString)")
                                }
                            }
                        }
                    }
                }
            }
            .frame(maxWidth: 760, alignment: .leading)
            .padding(20)
        }
        .alert(text("error.title"), isPresented: Binding(
            get: { !showsManualConnection && model.selectionError != nil },
            set: { if !$0 { model.clearSelectionError() } }
        )) {
            Button(text("dismiss"), role: .cancel) {}
        } message: {
            Text(model.selectionError ?? "")
        }
        .sheet(isPresented: $showsManualConnection) {
            ManualConnectionSheet(
                model: model,
                address: $address,
                language: language
            )
        }
    }
}

private struct ManualConnectionSheet: View {
    @ObservedObject var model: ClientModel
    @Binding var address: String
    let language: String

    @Environment(\.dismiss) private var dismiss
    @FocusState private var addressFocused: Bool

    var body: some View {
        let text = L10n(language)
        VStack(spacing: 0) {
            HStack {
                Text(text("connect.by.address"))
                    .font(.title2.bold())
                Spacer()
                Button(text("cancel"), role: .cancel) {
                    close()
                }
                .frame(minWidth: 44, minHeight: 44)
                .accessibilityIdentifier("dismiss-manual-address")
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 10)

            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
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
                            connect()
                        }

                    Button {
                        connect()
                    } label: {
                        Label(text("verify.connect"), systemImage: "checkmark.shield")
                            .frame(maxWidth: .infinity, minHeight: 44)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.isConnecting || address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("connect-server")

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

                    Text(text("http.notice"))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .padding(20)
            }
            .scrollDismissesKeyboard(.interactively)
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
        .interactiveDismissDisabled(model.isConnecting)
        .alert(text("error.title"), isPresented: Binding(
            get: { model.currentServer == nil && model.selectionError != nil },
            set: { if !$0 { model.clearSelectionError() } }
        )) {
            Button(text("dismiss"), role: .cancel) {}
        } message: {
            Text(model.selectionError ?? "")
        }
        .onAppear {
            addressFocused = true
        }
        .onDisappear {
            if model.isConnecting {
                model.cancelConnection()
            }
        }
        .onChange(of: model.currentServer) { _, server in
            if server != nil {
                dismiss()
            }
        }
    }

    private func connect() {
        addressFocused = false
        model.connect(address)
    }

    private func close() {
        addressFocused = false
        if model.isConnecting {
            model.cancelConnection()
        }
        dismiss()
    }
}

private struct ChooserSectionCard<Content: View>: View {
    private let content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            content
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 18))
        .overlay {
            RoundedRectangle(cornerRadius: 18)
                .stroke(Color.primary.opacity(0.06))
        }
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
                    .foregroundStyle(verified ? Color.accentColor : Color(uiColor: .secondaryLabel))
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
            .background(Color(uiColor: .systemBackground), in: RoundedRectangle(cornerRadius: 14))
            .overlay {
                RoundedRectangle(cornerRadius: 14)
                    .stroke(Color.primary.opacity(0.05))
            }
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(server.name), \(server.origin), \(verified ? text("verified") : text("verify.on.connect"))")
    }
}
