import Foundation

struct MobileSession: Decodable {
    let accessToken: String
    let tokenType: String
    let expiresAt: Int64
    let athlete: MobileAthlete

    private enum CodingKeys: String, CodingKey {
        case accessToken = "access_token"
        case tokenType = "token_type"
        case expiresAt = "expires_at"
        case athlete
    }
}

struct MobileAthlete: Decodable {
    let id: Int64
    let name: String
}

struct MobileMe: Decodable {
    let userID: Int64
    let connected: Bool
    let athlete: MobileAthlete
    let activitiesURL: String

    private enum CodingKeys: String, CodingKey {
        case userID = "user_id"
        case connected
        case athlete
        case activitiesURL = "activities_url"
    }
}

struct MobileActivities: Decodable {
    let activities: [MobileActivity]
    let sync: MobileSyncStatus?
    let nextCursor: String?

    private enum CodingKeys: String, CodingKey {
        case activities, sync
        case nextCursor = "next_cursor"
    }
}

struct MobileSyncStatus: Decodable {
    let message: String
    let pending: Int
    let completed: Int
    let failed: Int
    let blocked: Int
    let nextRunAt: Int64?
    let errors: [MobileSyncError]

    private enum CodingKeys: String, CodingKey {
        case message, pending, completed, failed, blocked, errors
        case nextRunAt = "next_run_at"
    }
}

struct MobileSyncError: Decodable, Identifiable {
    let jobID: Int64
    let message: String
    var id: Int64 { jobID }

    private enum CodingKeys: String, CodingKey {
        case jobID = "job_id"
        case message
    }
}

struct MobileActivity: Decodable, Identifiable {
    let id: Int64
    let name: String
    let type: String
    let typeLabel: String
    let startTime: String
    let distance: String
    let duration: String
    let stopCount: Int
    let lightStops: Int
    let roadCrossings: Int
    let detectedFactCount: Int
    let gpsStatus: String?
    let photoURL: String?

    private enum CodingKeys: String, CodingKey {
        case id
        case name
        case type
        case typeLabel = "type_label"
        case startTime = "start_time"
        case distance
        case duration
        case stopCount = "stop_count"
        case lightStops = "light_stops"
        case roadCrossings = "road_crossings"
        case detectedFactCount = "detected_fact_count"
        case photoURL = "photo_url"
        case gpsStatus = "gps_status"
    }
}

struct GrantExchangeRequest: Encodable {
    let grant: String
}

final class APIClient {
    private let session = URLSession.shared
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()

    func exchangeGrant(baseURL: URL, grant: String) async throws -> MobileSession {
        let body = try encoder.encode(GrantExchangeRequest(grant: grant))
        var request = URLRequest(url: baseURL.appending(path: "/api/mobile/session/exchange"))
        request.httpMethod = "POST"
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        return try await perform(request, as: MobileSession.self)
    }

    func fetchMe(baseURL: URL, accessToken: String) async throws -> MobileMe {
        var request = URLRequest(url: baseURL.appending(path: "/api/mobile/me"))
        request.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        return try await perform(request, as: MobileMe.self)
    }

    func fetchActivities(baseURL: URL, accessToken: String, limit: Int, before: String? = nil) async throws -> MobileActivities {
        var components = URLComponents(url: baseURL.appending(path: "/api/mobile/activities"), resolvingAgainstBaseURL: false)
        components?.queryItems = [URLQueryItem(name: "limit", value: String(limit))]
        if let before {
            components?.queryItems?.append(URLQueryItem(name: "before", value: before))
        }
        guard let url = components?.url else {
            throw APIError.invalidURL
        }
        var request = URLRequest(url: url)
        request.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        return try await perform(request, as: MobileActivities.self)
    }

    func retryActivities(baseURL: URL, accessToken: String) async throws -> MobileSyncStatus {
        var request = URLRequest(url: baseURL.appending(path: "/api/sync"))
        request.httpMethod = "POST"
        request.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        return try await perform(request, as: MobileSyncStatus.self)
    }

    private func perform<T: Decodable>(_ request: URLRequest, as type: T.Type) async throws -> T {
        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIError.invalidResponse
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = String(data: data, encoding: .utf8) ?? "request failed"
            throw APIError.server(httpResponse.statusCode, message)
        }
        return try decoder.decode(T.self, from: data)
    }
}

enum APIError: LocalizedError {
    case invalidURL
    case invalidResponse
    case server(Int, String)

    var errorDescription: String? {
        switch self {
        case .invalidURL:
            return "The backend URL is invalid."
        case .invalidResponse:
            return "The backend response was invalid."
        case let .server(status, message):
            let compact = message.trimmingCharacters(in: .whitespacesAndNewlines)
            return "Backend error \(status): \(compact)"
        }
    }
}
