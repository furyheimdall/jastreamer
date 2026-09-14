import SwiftUI

@main
@MainActor
struct JastreamerApp: App {
    @StateObject private var bootstrap = ClientBootstrap()

    var body: some Scene {
        WindowGroup {
            BootstrapView(bootstrap: bootstrap)
        }
    }
}

private struct BootstrapView: View {
    @ObservedObject var bootstrap: ClientBootstrap

    var body: some View {
        if let recentServers = bootstrap.recentServers {
            ClientRootView(recentServers: recentServers)
        } else {
            let text = L10n(L10n.bootstrapLanguage)
            VStack(spacing: 20) {
                BrandMark()
                    .frame(width: 88, height: 88)
                Text(text("storage.title"))
                    .font(.title2.bold())
                Text([text("storage.message"), bootstrap.error].compactMap { $0 }.joined(separator: "\n"))
                    .multilineTextAlignment(.center)
                    .foregroundStyle(.secondary)
                Button(text("retry")) {
                    bootstrap.reload()
                }
                .buttonStyle(.borderedProminent)
                .frame(minHeight: 44)
            }
            .padding(32)
        }
    }
}

private struct ClientRootView: View {
    @StateObject private var model: ClientModel
    @Environment(\.scenePhase) private var scenePhase

    init(recentServers: RecentServers) {
        _model = StateObject(wrappedValue: ClientModel(recentServers: recentServers))
    }

    var body: some View {
        ClientShell(model: model)
            .onAppear {
                if scenePhase == .active {
                    model.activate()
                }
            }
            .onDisappear {
                model.deactivate()
            }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active {
                    model.activate()
                } else {
                    model.deactivate()
                }
            }
    }
}

private struct ClientShell: View {
    @ObservedObject var model: ClientModel
    @ObservedObject private var recentServers: RecentServers
    @ObservedObject private var discovery: ServerDiscovery
    @Environment(\.scenePhase) private var scenePhase

    init(model: ClientModel) {
        self.model = model
        _recentServers = ObservedObject(wrappedValue: model.recentServers)
        _discovery = ObservedObject(wrappedValue: model.discovery)
    }

    var body: some View {
        let text = L10n(recentServers.language)
        VStack(spacing: 0) {
            ClientHeader(model: model, language: recentServers.language)
            Divider()
            if let server = model.currentServer {
                RemoteControlView(
                    model: model,
                    server: server,
                    language: recentServers.language,
                    isActive: scenePhase == .active
                )
                .id(model.webAttemptID)
            } else {
                ServerChooserView(
                    model: model,
                    recentServers: recentServers,
                    discovery: discovery,
                    language: recentServers.language
                )
            }
        }
        // UIKit's keyboard layout guide sizes the Web viewport; native selection uses SwiftUI.
        .ignoresSafeArea(.keyboard, edges: model.currentServer == nil ? [] : .bottom)
        .background(Color(uiColor: .systemBackground))
        .tint(Color(red: 0.20, green: 0.42, blue: 0.29))
        .alert(text("error.title"), isPresented: Binding(
            get: { model.currentServer == nil && model.selectionError != nil },
            set: { if !$0 { model.clearSelectionError() } }
        )) {
            Button(text("dismiss"), role: .cancel) {}
        } message: {
            Text(model.selectionError ?? "")
        }
    }
}

private struct ClientHeader: View {
    @ObservedObject var model: ClientModel
    let language: String

    var body: some View {
        let text = L10n(language)
        HStack(spacing: 10) {
            BrandMark()
                .frame(width: 36, height: 36)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 1) {
                Text(model.currentServer?.name ?? "jastreamer")
                    .font(.headline)
                    .lineLimit(1)
                Text(model.currentServer?.origin ?? text("ios.control"))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("current-server")

            if model.currentServer != nil {
                Button {
                    model.switchServer()
                } label: {
                    Label(text("switch.server"), systemImage: "server.rack")
                        .labelStyle(.iconOnly)
                        .frame(width: 44, height: 44)
                }
                .accessibilityLabel(text("switch.server"))
                .accessibilityIdentifier("switch-server")
            }

            Menu {
                Button {
                    model.changeLanguage("en")
                } label: {
                    if language == "en" {
                        Label("English", systemImage: "checkmark")
                    } else {
                        Text("English")
                    }
                }
                Button {
                    model.changeLanguage("ko")
                } label: {
                    if language == "ko" {
                        Label("한국어", systemImage: "checkmark")
                    } else {
                        Text("한국어")
                    }
                }
            } label: {
                Label(text("language"), systemImage: "globe")
                    .labelStyle(.iconOnly)
                    .frame(width: 44, height: 44)
            }
            .accessibilityLabel(text("language"))
            .accessibilityIdentifier("language-menu")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(.bar)
    }
}

struct BrandMark: View {
    var body: some View {
        Image("BrandMark")
            .resizable()
            .scaledToFit()
    }
}
