import Foundation

struct MobileActivityDetail: Decodable {
    let activity: MobileActivity
    let stops: [MobileStop]
    let statsStatus: String
    let stopTotal: String?
    let paceLabel: String?
    let paceValue: String?
    let paceUnit: String?
    let averageHeartRate: Double?
    let averagePower: Double?
    let pacing: MobilePacing?
    enum CodingKeys: String, CodingKey {
        case activity, stops, pacing
        case statsStatus = "stats_status", stopTotal = "stop_total"
        case paceLabel = "pace_label", paceValue = "pace_value", paceUnit = "pace_unit"
        case averageHeartRate = "average_heart_rate", averagePower = "average_power"
    }
}

struct MobileStop: Decodable {
    let lat: Double
    let lon: Double
    let startSeconds: Double
    let duration: String
    let durationSeconds: Int
    let hasTrafficLight: Bool
    let hasRoadCrossing: Bool
    let crossingRoad: String?
    enum CodingKeys: String, CodingKey {
        case lat, lon, duration
        case startSeconds = "start_seconds", durationSeconds = "duration_seconds"
        case hasTrafficLight = "has_traffic_light", hasRoadCrossing = "has_road_crossing", crossingRoad = "crossing_road"
    }
}

struct MobilePacing: Decodable {
    let distance: String
    let fastestFiveKM: String
    let fastestFiveFrom: String
    let finishChange: String
    let finishDetail: String
    let fastestSplit: String
    let splits: [MobileSplit]?
    enum CodingKeys: String, CodingKey {
        case distance = "Distance", fastestFiveKM = "FastestFiveKM", fastestFiveFrom = "FastestFiveFrom"
        case finishChange = "FinishChange", finishDetail = "FinishDetail", fastestSplit = "FastestSplit", splits = "Splits"
    }
}
struct MobileSplit: Decodable {
    let label: String
    let distance: String
    let duration: String
    let speed: String
    let power: String
    let heartRate: String
    let hasPower: Bool
    let hasHeartRate: Bool
    let fastest: Bool
    enum CodingKeys: String, CodingKey {
        case label = "Label", distance = "Distance", duration = "Duration", speed = "Speed", power = "Power", heartRate = "HeartRate"
        case hasPower = "HasPower", hasHeartRate = "HasHeartRate", fastest = "Fastest"
    }
}
