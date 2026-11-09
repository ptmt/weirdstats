import SwiftUI

@main
struct WeirdStatsApp: App {
    @StateObject private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            ContentView()
                #if DEBUG
                .preferredColorScheme(ProcessInfo.processInfo.arguments.contains("--ui-test-dark") ? .dark : nil)
                .transformEnvironment(\.dynamicTypeSize) { size in
                    if ProcessInfo.processInfo.arguments.contains("--ui-test-large-type") { size = .accessibility5 }
                }
                #endif
                .environmentObject(model)
                .task {
                    await model.bootstrap()
                }
                .onOpenURL { url in
                    model.handleOpenURL(url)
                }
        }
    }
}
