import SwiftUI

struct WelcomeView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.colorScheme) private var colorScheme
    @State private var isShowingServerSettings = false

    private let stravaOrange = Color(red: 0.99, green: 0.30, blue: 0.01)

    var body: some View {
        GeometryReader { geometry in
            ScrollView {
                VStack(spacing: 0) {
                    HStack {
                        Spacer()
                        Button {
                            isShowingServerSettings = true
                        } label: {
                            Image(systemName: "gearshape")
                                .font(.title3)
                                .foregroundStyle(.secondary)
                                .frame(width: 44, height: 44)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Server settings")
                        .disabled(model.isAuthenticating)
                    }

                    Spacer(minLength: 24)

                    introduction

                    VStack(alignment: .leading, spacing: 20) {
                        feature("Stops and pauses", symbol: "pause.circle", color: .purple)
                        feature("Road crossings", symbol: "point.topleft.down.to.point.bottomright.curvepath", color: .teal)
                        feature("Unexpected activity facts", symbol: "sparkles", color: .pink)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(24)
                    .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 24))
                    .padding(.top, 32)

                    Spacer(minLength: 40)

                    connection
                }
                .frame(maxWidth: 440)
                .padding(.horizontal, 28)
                .padding(.top, 8)
                .padding(.bottom, 24)
                .frame(maxWidth: .infinity)
                .frame(minHeight: geometry.size.height)
            }
            .background(backgroundColor.ignoresSafeArea())
        }
        .sheet(isPresented: $isShowingServerSettings) {
            serverSettings
        }
    }

    private var backgroundColor: Color {
        colorScheme == .dark
            ? Color(red: 0.063, green: 0.078, blue: 0.098)
            : Color(red: 0.961, green: 0.969, blue: 0.976)
    }

    private var introduction: some View {
        VStack(spacing: 16) {
            // Bundle the website's original artwork and match its dark-mode inversion.
            Group {
                if colorScheme == .dark {
                    logo.colorInvert()
                } else {
                    logo
                }
            }
            .frame(maxWidth: 340)
            .frame(height: 160)
            .accessibilityLabel("WeirdStats")

            Text("Optional stats for your Strava activities")
                .font(.system(.largeTitle, design: .rounded, weight: .bold))
                .foregroundStyle(.primary)
                .accessibilityAddTraits(.isHeader)

            Text("Weirdstats listens to new activities, extracts these stats, and can write them back to the description. Turn any of them off in settings.")
                .font(.body)
                .foregroundStyle(.secondary)
                .lineSpacing(3)
        }
        .multilineTextAlignment(.center)
        .fixedSize(horizontal: false, vertical: true)
    }

    @ViewBuilder
    private var logo: some View {
        if let image = UIImage(named: "weirdstats.png", in: .main, compatibleWith: nil) {
            Image(uiImage: image)
                .resizable()
                .scaledToFit()
        }
    }

    private func feature(_ title: String, symbol: String, color: Color) -> some View {
        HStack(spacing: 14) {
            Image(systemName: symbol)
                .font(.title3.weight(.medium))
                .foregroundStyle(color)
                .frame(width: 28)
                .accessibilityHidden(true)
            Text(title)
                .font(.subheadline.weight(.medium))
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var connection: some View {
        VStack(spacing: 14) {
            if !model.errorMessage.isEmpty {
                Label {
                    Text(model.errorMessage)
                } icon: {
                    Image(systemName: "exclamationmark.circle")
                }
                .font(.footnote)
                .foregroundStyle(.red)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(16)
                .background(Color.red.opacity(0.08), in: RoundedRectangle(cornerRadius: 16))
            }

            Button {
                Task { await model.signIn() }
            } label: {
                HStack(spacing: 10) {
                    if model.isAuthenticating {
                        ProgressView()
                            .tint(.white)
                    }
                    Text(model.isAuthenticating ? "Connecting…" : "Connect Strava")
                        .font(.headline)
                }
                .foregroundStyle(.white)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 19)
                .background(stravaOrange, in: RoundedRectangle(cornerRadius: 18))
            }
            .buttonStyle(.plain)
            .disabled(model.isAuthenticating)

            Text("Connect your account to get started.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
    }

    private var serverSettings: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("https://weirdstats.com", text: $model.serverURLText)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                        .accessibilityLabel("Server URL")
                } header: {
                    Text("Server URL")
                } footer: {
                    Text("Use weirdstats.com, or enter the address of your own WeirdStats server.")
                }
            }
            .navigationTitle("Server settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { isShowingServerSettings = false }
                }
            }
        }
    }
}
