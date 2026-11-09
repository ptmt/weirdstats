import SwiftUI

@MainActor final class ActivityDetailModel: ObservableObject {
    @Published private(set) var detail: MobileActivityDetail?
    @Published private(set) var isLoading = false
    @Published private(set) var errorMessage = ""
    private var requestID = UUID()

    func cancel() {
        requestID = UUID()
        isLoading = false
        detail = nil
        errorMessage = ""
    }

    func load(id: Int64, app: AppModel) async {
        let request = UUID()
        let session = app.detailSessionID
        requestID = request
        isLoading = true
        detail = nil
        errorMessage = ""
        defer { if requestID == request { isLoading = false } }
        do {
            let value = try await app.fetchDetail(id: id)
            guard requestID == request, session == app.detailSessionID, !Task.isCancelled else { return }
            detail = value
        } catch {
            guard requestID == request, session == app.detailSessionID, !Task.isCancelled, !(error is CancellationError) else { return }
            if case APIError.server(404, _) = error {
                errorMessage = "This activity is unavailable, does not belong to this account, or this server needs the activity-detail update."
            } else if case APIError.server(401, _) = error {
                errorMessage = "Your session expired. Reconnect Strava in Profile."
            } else { errorMessage = "Couldn’t load this activity. Check your connection and try again." }
        }
    }
}
