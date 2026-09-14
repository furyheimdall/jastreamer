import SwiftUI

struct RemoteControlView: View {
    @ObservedObject var model: ClientModel
    let server: ServerEndpoint
    let language: String
    let isActive: Bool

    @StateObject private var webController: RestrictedWebController

    init(model: ClientModel, server: ServerEndpoint, language: String, isActive: Bool) {
        self.model = model
        self.server = server
        self.language = language
        self.isActive = isActive
        _webController = StateObject(
            wrappedValue: RestrictedWebController(server: server, language: language)
        )
    }

    var body: some View {
        let text = L10n(language)
        let webActive = isActive && model.isRemoteVerified
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Button {
                    webController.goBack()
                } label: {
                    Label(text("web.back"), systemImage: "chevron.backward")
                        .labelStyle(.iconOnly)
                        .frame(width: 44, height: 44)
                }
                .disabled(!webController.canGoBack || model.isReconnecting || !webActive)
                .accessibilityLabel(text("web.back"))
                .accessibilityIdentifier("back-web")

                Button {
                    if webController.failure != nil {
                        model.retryRemote()
                    } else {
                        model.clearRemoteError()
                        webController.reload()
                    }
                } label: {
                    Label(text("web.reload"), systemImage: "arrow.clockwise")
                        .labelStyle(.iconOnly)
                        .frame(width: 44, height: 44)
                }
                .disabled(model.isReconnecting || !webActive)
                .accessibilityLabel(text("web.reload"))
                .accessibilityIdentifier("reload-web")

                Spacer()

                if webController.isLoading || model.isReconnecting {
                    ProgressView()
                        .controlSize(.small)
                    Text(model.isReconnecting ? text("reconnecting") : text("web.loading"))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(Color(uiColor: .secondarySystemBackground))

            ZStack {
                RestrictedWebSurface(
                    controller: webController,
                    active: webActive,
                    language: language,
                    onLanguageChanged: { value in
                        model.acceptWebLanguage(value)
                    }
                )

                .allowsHitTesting(webActive)
                .opacity(webActive ? 1 : 0)
                .accessibilityHidden(!webActive)

                if model.isReconnecting {
                    Color(uiColor: .systemBackground)
                        .opacity(0.97)
                    VStack(spacing: 14) {
                        ProgressView()
                        Text(text("reconnecting"))
                            .foregroundStyle(.secondary)
                    }
                    .padding(28)
                    .accessibilityElement(children: .combine)
                } else if let message = failureMessage(text: text) {
                    Color(uiColor: .systemBackground)
                        .opacity(0.97)
                    VStack(spacing: 16) {
                        Image(systemName: "wifi.exclamationmark")
                            .font(.system(size: 38))
                            .foregroundStyle(.secondary)
                        Text(text("web.failed.title"))
                            .font(.title2.bold())
                        Text(message)
                            .multilineTextAlignment(.center)
                            .foregroundStyle(.secondary)
                        Button {
                            model.retryRemote()
                        } label: {
                            Label(text("retry"), systemImage: "arrow.clockwise")
                                .frame(minHeight: 44)
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(!isActive)
                        .accessibilityIdentifier("retry-web")
                    }
                    .frame(maxWidth: 460)
                    .padding(28)
                }
            }
        }
    }

    private func failureMessage(text: L10n) -> String? {
        if let remoteError = model.remoteError {
            return remoteError
        }
        guard let failure = webController.failure else { return nil }
        if case let .response(status) = failure {
            return "\(text(failure.localizationKey)) \(status)"
        }
        return text(failure.localizationKey)
    }
}
