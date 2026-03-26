import SwiftUI

struct ContentView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        NavigationStack {
            Group {
                if model.isSignedIn {
                    signedInView
                } else {
                    signedOutView
                }
            }
            .navigationTitle("WeirdStats")
        }
    }

    private var signedOutView: some View {
        Form {
            Section("Backend") {
                TextField("https://your-weirdstats.example", text: $model.serverURLText)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
            }

            Section {
                Button(action: {
                    Task {
                        await model.signIn()
                    }
                }) {
                    if model.isAuthenticating {
                        ProgressView()
                            .frame(maxWidth: .infinity)
                    } else {
                        Text("Connect Strava")
                            .frame(maxWidth: .infinity)
                    }
                }
                .disabled(model.isAuthenticating)
            } footer: {
                Text("The app opens the backend mobile OAuth flow in ASWebAuthenticationSession and exchanges the short-lived grant for a backend bearer token.")
            }

            if !model.errorMessage.isEmpty {
                Section("Error") {
                    Text(model.errorMessage)
                        .font(.footnote)
                        .foregroundStyle(.red)
                }
            }
        }
    }

    private var signedInView: some View {
        List {
            Section {
                HStack {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(model.athleteName)
                            .font(.headline)
                        Text(model.serverURLText)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    if model.isLoading {
                        ProgressView()
                    }
                }
            }

            Section("Recent Activities") {
                if model.activities.isEmpty && !model.isLoading {
                    Text("No activities yet.")
                        .foregroundStyle(.secondary)
                }
                ForEach(model.activities) { activity in
                    ActivityRow(activity: activity)
                }
            }
        }
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Button("Refresh") {
                    Task {
                        await model.refresh()
                    }
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button("Sign Out") {
                    model.signOut()
                }
            }
        }
        .overlay(alignment: .bottom) {
            if !model.errorMessage.isEmpty {
                Text(model.errorMessage)
                    .font(.footnote)
                    .foregroundStyle(.red)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .background(.thinMaterial, in: Capsule())
                    .padding()
            }
        }
    }
}

private struct ActivityRow: View {
    let activity: MobileActivity

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(activity.name)
                        .font(.headline)
                    Text("\(activity.typeLabel) • \(activity.startTime)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Text(activity.distance)
                    .font(.system(.subheadline, design: .rounded).weight(.semibold))
            }

            HStack(spacing: 12) {
                statCapsule(activity.duration)
                statCapsule("\(activity.stopCount) stops")
                statCapsule("\(activity.detectedFactCount) facts")
                if activity.roadCrossings > 0 {
                    statCapsule("\(activity.roadCrossings) crossings")
                }
            }
            .font(.caption2.monospacedDigit())
        }
        .padding(.vertical, 4)
    }

    private func statCapsule(_ text: String) -> some View {
        Text(text)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(Color(.secondarySystemFill), in: Capsule())
    }
}
