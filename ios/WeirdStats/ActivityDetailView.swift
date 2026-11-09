import SwiftUI
import MapKit

struct ActivityDetailView: View {
    let activityID: Int64
    let focusedFactID: String?
    @EnvironmentObject private var app: AppModel
    @StateObject private var state = ActivityDetailModel()
    @State private var selectedFactID: String?

    init(activityID: Int64, focusedFactID: String? = nil) {
        self.activityID = activityID
        self.focusedFactID = focusedFactID
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                if state.isLoading { ProgressView("Loading ride details…").frame(maxWidth: .infinity).padding(40) }
                if !state.errorMessage.isEmpty {
                    ContentUnavailableView {
                        Label("Couldn’t load activity", systemImage: "exclamationmark.triangle")
                    } description: { Text(state.errorMessage) } actions: {
                        Button("Try again") { Task { await state.load(id: activityID, app: app) } }
                    }
                }
                if let detail = state.detail {
                    Text(detail.activity.name).font(.largeTitle.bold())
                    Label(detail.activity.typeLabel, systemImage: detail.activity.type.lowercased().contains("ride") ? "bicycle" : "figure.run").foregroundStyle(.secondary)
                    Text(formattedDate(detail.activity.startTime)).font(.subheadline).foregroundStyle(.secondary)
                    routeSection(detail)
                    metricsSection(detail)
                    factsSection(detail)
                    stopsSection(detail)
                    if let pacing = detail.pacing { pacingSection(pacing) }
                }
            }.padding()
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle("Ride details")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: activityID) {
            selectedFactID = focusedFactID
            await state.load(id: activityID, app: app)
        }
        .onChange(of: app.detailSessionID) { _, _ in state.cancel() }
        .onDisappear { state.cancel() }
        .refreshable { await state.load(id: activityID, app: app) }
    }

    private func formattedDate(_ raw: String) -> String {
        guard let date = ISO8601DateFormatter().date(from: raw) else { return raw }
        return date.formatted(date: .complete, time: .shortened)
    }

    private func routeSection(_ detail: MobileActivityDetail) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Route").font(.title2.bold())
            let coordinates = (detail.activity.route ?? []).filter(\.isValid).map { CLLocationCoordinate2D(latitude: $0.lat, longitude: $0.lon) }
            if coordinates.count > 1 {
                Map {
                    MapPolyline(coordinates: coordinates).stroke(.purple, lineWidth: 4)
                    Marker("Start", coordinate: coordinates[0]).tint(.green)
                    Marker("Finish", coordinate: coordinates[coordinates.count - 1]).tint(.red)
                    if let fact = detail.activity.facts?.first(where: { $0.id == selectedFactID }) {
                        let path = (fact.path ?? []).filter(\.isValid).map { CLLocationCoordinate2D(latitude: $0.lat, longitude: $0.lon) }
                        if path.count > 1 { MapPolyline(coordinates: path).stroke(.orange, lineWidth: 7) }
                        ForEach(Array((fact.points ?? []).enumerated()), id: \.offset) { _, point in
                            if RoutePoint(lat: point.lat, lon: point.lon).isValid {
                                Marker(point.label ?? fact.title, coordinate: CLLocationCoordinate2D(latitude: point.lat, longitude: point.lon)).tint(.orange)
                            }
                        }
                    }
                    ForEach(Array(detail.stops.enumerated()), id: \.offset) { index, stop in
                        if RoutePoint(lat: stop.lat, lon: stop.lon).isValid {
                            Annotation("Stop \(index + 1)", coordinate: CLLocationCoordinate2D(latitude: stop.lat, longitude: stop.lon)) {
                                Image(systemName: "pause.circle.fill").foregroundStyle(.white, .purple).font(.title3).accessibilityLabel("Stop \(index + 1), \(stop.duration)")
                            }
                        }
                    }
                }
                .mapStyle(.standard(pointsOfInterest: .excludingAll))
                .frame(height: 330)
                .clipShape(RoundedRectangle(cornerRadius: 18))
                .accessibilityIdentifier("activity-detail-map")
                Text("Full stored GPS route · pinch to zoom and drag to explore. Tap a fact below to highlight its segment or locations.").font(.caption).foregroundStyle(.secondary)
            } else {
                ContentUnavailableView(detail.activity.type.lowercased().contains("virtual") ? "Virtual activity · no outdoor GPS route" : "No GPS route recorded", systemImage: "map", description: Text("Metrics and detected facts are still available when recorded."))
            }
        }
    }

    private func metricsSection(_ detail: MobileActivityDetail) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Activity").font(.title2.bold())
            LabeledContent("Distance", value: detail.activity.distance)
            LabeledContent("Moving time", value: detail.activity.duration)
            if let label = detail.paceLabel, let value = detail.paceValue { LabeledContent(label, value: value + " " + (detail.paceUnit ?? "")) }
            if let hr = detail.averageHeartRate { LabeledContent("Average heart rate", value: "\(hr.formatted(.number.precision(.fractionLength(0)))) bpm") }
            if let power = detail.averagePower { LabeledContent("Average power", value: "\(power.formatted(.number.precision(.fractionLength(0)))) W") }
            if detail.statsStatus == "available" {
                LabeledContent("Stops", value: String(detail.activity.stopCount))
                if let total = detail.stopTotal { LabeledContent("Stopped time", value: total) }
                LabeledContent("Traffic-light stops", value: String(detail.activity.lightStops))
                LabeledContent("Road crossings", value: String(detail.activity.roadCrossings))
            } else { Text("Stop analysis is waiting for processing.").font(.caption).foregroundStyle(.secondary) }
        }.padding().background(.background, in: RoundedRectangle(cornerRadius: 18))
    }

    private func factsSection(_ detail: MobileActivityDetail) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Ride facts & segments").font(.title2.bold())
            if (detail.activity.facts ?? []).isEmpty {
                Text(detail.activity.factsStatus == "pending" ? "Facts are waiting for server processing." : "No facts were detected for this activity.").foregroundStyle(.secondary)
            }
            ForEach(detail.activity.facts ?? []) { fact in
                Button { selectedFactID = selectedFactID == fact.id ? nil : fact.id } label: {
                    VStack(alignment: .leading, spacing: 8) {
                        HStack { Text(fact.title).font(.headline); Spacer(); if let badge = fact.badgeLabel { Text(badge).font(.caption).foregroundStyle(.purple) } }
                        Text(fact.summary).font(.subheadline)
                        if !(fact.path ?? []).isEmpty || !(fact.points ?? []).isEmpty { Label(selectedFactID == fact.id ? "Highlighted on route" : "Highlight on route", systemImage: "map").font(.caption).foregroundStyle(.purple) }
                    }.frame(maxWidth: .infinity, alignment: .leading).padding()
                        .background(selectedFactID == fact.id ? Color.purple.opacity(0.1) : Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 16))
                }.buttonStyle(.plain)
            }
        }
    }

    private func stopsSection(_ detail: MobileActivityDetail) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Stop details").font(.title2.bold())
            if detail.stops.isEmpty { Text(detail.statsStatus == "available" ? "No stored stops for this activity." : "Stop details are not processed yet.").foregroundStyle(.secondary) }
            ForEach(Array(detail.stops.enumerated()), id: \.offset) { index, stop in
                VStack(alignment: .leading, spacing: 6) {
                    HStack { Label("Stop \(index + 1)", systemImage: stop.hasTrafficLight ? "light.beacon.max" : "pause.circle"); Spacer(); Text(stop.duration).bold() }
                    Text("\(Duration.seconds(stop.startSeconds).formatted(.time(pattern: .hourMinuteSecond))) after start").font(.caption).foregroundStyle(.secondary)
                    if stop.hasTrafficLight { Text("Traffic light").font(.caption) }
                    if stop.hasRoadCrossing { Text(stop.crossingRoad.map { "Road crossing · \($0)" } ?? "Road crossing").font(.caption) }
                }.padding().background(.background, in: RoundedRectangle(cornerRadius: 14))
            }
        }
    }

    private func pacingSection(_ pacing: MobilePacing) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Ride splits").font(.title2.bold())
            Text("Calculated from the same stored GPS trace as the website. Split durations include pauses.").font(.caption).foregroundStyle(.secondary)
            if !pacing.fastestFiveKM.isEmpty { LabeledContent("Fastest 5 km", value: pacing.fastestFiveKM) }
            if !pacing.finishChange.isEmpty { Text(pacing.finishChange).font(.headline); Text(pacing.finishDetail).font(.caption) }
            ForEach(Array((pacing.splits ?? []).enumerated()), id: \.offset) { _, split in
                VStack(alignment: .leading, spacing: 6) {
                    HStack { Text(split.label).font(.headline); Spacer(); if split.fastest { Text("Fastest").font(.caption).foregroundStyle(.purple) } }
                    Text("\(split.distance) · \(split.duration) · \(split.speed)")
                    if split.hasPower { Text(split.power).font(.caption) }
                    if split.hasHeartRate { Text(split.heartRate).font(.caption) }
                }.padding().background(.background, in: RoundedRectangle(cornerRadius: 14))
            }
        }
    }
}
