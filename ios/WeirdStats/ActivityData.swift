import Foundation

struct RoutePoint: Decodable {
    let lat: Double
    let lon: Double
    var isValid: Bool { lat.isFinite && lon.isFinite && (-90...90).contains(lat) && (-180...180).contains(lon) }
}

struct MobileFact: Decodable, Identifiable {
    let id: String
    let kind: String
    let title: String
    let summary: String
    let badgeLabel: String?
    let path: [RoutePoint]?
    let points: [FactPoint]?
    struct FactPoint: Decodable {
        let lat: Double
        let lon: Double
        let label: String?
    }
    private enum CodingKeys: String, CodingKey {
        case id, kind, title, summary, path, points
        case badgeLabel = "badge_label"
    }
}

// The website's server-side effort calendar is authoritative (including timezone,
// normalized effort, fallback moving hours, future days and intensity levels).
struct MobileHistory: Decodable {
    let days: [HistoryDay]
    let year: Int
    let totalEffort: Double
    private enum CodingKeys: String, CodingKey {
        case days = "Days", year = "Year", totalEffort = "TotalEffort"
    }
}

struct HistoryDay: Decodable, Identifiable {
    let date: String
    let tooltip: String
    let level: Int
    let inRange: Bool
    var id: String { date }
    private enum CodingKeys: String, CodingKey {
        case date = "Date", tooltip = "Tooltip", level = "Level", inRange = "InRange"
    }
}
