import Foundation
import Network

// Explicit synthetic HTTP fixture. Never included in the application target.
final class RideTestServer {
    private let listener: NWListener
    private let state = State()
    let url: String

    private final class State {
        let lock = NSLock()
        var delay: TimeInterval = 0
        var requests: [String] = []
        var connections: [NWConnection] = []
    }
    var requests: [String] {
        state.lock.lock(); defer { state.lock.unlock() }
        return state.requests
    }
    func delayResponses(_ seconds: TimeInterval) {
        state.lock.lock(); defer { state.lock.unlock() }
        state.delay = seconds
    }
    init() throws {
        listener = try NWListener(using: .tcp, on: .any)
        let ready = DispatchSemaphore(value: 0)
        listener.stateUpdateHandler = { state in if case .ready = state { ready.signal() } }
        listener.newConnectionHandler = { [state] connection in
            state.lock.lock()
            state.connections.append(connection)
            state.lock.unlock()
            connection.start(queue: .global())
            connection.receive(minimumIncompleteLength: 1, maximumLength: 8192) { data, _, _, _ in
                let path = String(data: data ?? Data(), encoding: .utf8)?.components(separatedBy: " ").dropFirst().first ?? ""
                let body = Self.body(path: path)
                let response = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nCache-Control: no-store\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)"
                let reply = { connection.send(content: Data(response.utf8), completion: .contentProcessed { _ in connection.cancel() }) }
                state.lock.lock()
                state.requests.append(String(data: data ?? Data(), encoding: .utf8)?.components(separatedBy: "\r\n").first ?? "")
                let delay = state.delay
                state.lock.unlock()
                DispatchQueue.global().asyncAfter(deadline: .now() + delay, execute: reply)
            }
        }
        listener.start(queue: .global())
        guard ready.wait(timeout: .now() + 5) == .success, let port = listener.port else { throw NSError(domain: "RideTestServer", code: 1) }
        url = "http://127.0.0.1:\(port.rawValue)"
    }
    deinit {
        listener.cancel()
        state.lock.lock()
        let connections = state.connections
        state.connections = []
        state.lock.unlock()
        connections.forEach { $0.cancel() }
    }
    static func activity(_ id: Int) -> [String: Any] {
        let route = id == 42 ? [["lat": 52.5, "lon": 13.4], ["lat": 52.51, "lon": 13.41]] : []
        return ["id": id, "name": "Test ride \(id)", "type": "Ride", "type_label": "Ride", "start_time": "2025-01-02T12:00:00Z", "distance": "1 km", "duration": "5m", "stop_count": 0, "light_stops": 0, "road_crossings": 0, "detected_fact_count": 1, "route": route, "route_status": id == 42 ? "available" : "no_gps", "facts_status": "available", "facts": [["id": "segment", "kind": "segment", "title": "Stored test segment", "summary": "Synthetic test evidence only", "path": route]]]
    }
    static func body(path: String) -> String {
        let object: [String: Any]
        if path == "/api/mobile/me" {
            object = ["user_id": 1, "connected": true, "athlete": ["id": 1, "name": "UI test account"], "activities_url": "/api/mobile/activities"]
        } else if path == "/api/mobile/activities/42" || path == "/api/mobile/activities/43" {
            object = ["activity": activity(path.hasSuffix("42") ? 42 : 43), "stops": [], "stats_status": "available", "stop_total": "0s"]
        } else if path.contains("year=") {
            let components = URLComponents(string: "http://fixture\(path)")
            let year = Int(components?.queryItems?.first(where: { $0.name == "year" })?.value ?? "") ?? 2024
            var calendar = Calendar(identifier: .gregorian)
            calendar.timeZone = TimeZone(secondsFromGMT: 0)!
            let start = calendar.date(from: DateComponents(year: year, month: 1, day: 1))!
            let count = calendar.range(of: .day, in: .year, for: start)!.count
            let days: [[String: Any]] = (0..<count).map { offset in
                let date = calendar.date(byAdding: .day, value: offset, to: start)!
                let c = calendar.dateComponents([.month, .day], from: date)
                let key = String(format: "%04d-%02d-%02d", year, c.month!, c.day!)
                let tooltip = c.month == 1 && c.day == 1 ? "Jan 1 · 2.5 effort hours · fixture" :
                    (c.month == 2 && c.day == 29 ? "Feb 29 · 1.5 effort hours · fixture" : "\(key) · synthetic fixture")
                return ["Date": key, "Tooltip": tooltip, "Level": offset % 9, "InRange": true]
            }
            object = ["activities": [], "years": [2025, 2024],
                      "history": ["Year": year, "TotalEffort": year == 2024 ? 4.0 : 3.0, "Days": days]]
        } else { object = ["activities": [activity(42), activity(43)]] }
        return String(data: try! JSONSerialization.data(withJSONObject: object), encoding: .utf8)!
    }
}
