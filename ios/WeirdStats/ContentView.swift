import SwiftUI
import MapKit

struct ContentView: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("weirdstats.selected-tab") private var selectedTab = "activities"

    var body: some View {
        if model.isSignedIn {
            TabView(selection: $selectedTab) {
                NavigationStack { ActivitiesView() }
                    .tabItem { Label("Activities", systemImage: "figure.run") }.tag("activities")
                NavigationStack { YearView() }
                    .tabItem { Label("Year", systemImage: "square.grid.3x3.fill") }.tag("year")
                NavigationStack { FactsView() }
                    .tabItem { Label("Facts", systemImage: "sparkles") }.tag("facts")
                NavigationStack { ProfileView() }
                    .tabItem { Label("Profile", systemImage: "person.crop.circle") }.tag("profile")
            }
            .tint(.purple)
        } else {
            NavigationStack { WelcomeView().toolbar(.hidden, for: .navigationBar) }
        }
    }
}

private struct ActivitiesView: View {
    @EnvironmentObject private var model: AppModel
    var body: some View {
        ScrollView {
            LazyVStack(spacing: 18) {
                if !model.errorMessage.isEmpty { ErrorNotice(message: model.errorMessage) { await model.refresh() } }
                if model.isLoading && model.activities.isEmpty { ProgressView("Loading activities…").padding(40) }
                if model.activities.isEmpty && !model.isLoading && model.errorMessage.isEmpty {
                    ContentUnavailableView("No activities yet", systemImage: "figure.run", description: Text("Imported Strava activities will appear here. Check import progress in Profile."))
                }
                ForEach(model.activities) { activity in
                    NavigationLink { ActivityDetailView(activityID: activity.id) } label: { ActivityCard(activity: activity) }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("activity-card-\(activity.id)")
                }
                if model.nextCursor != nil {
                    Button("Load older activities") { Task { await model.loadMore() } }.disabled(model.isLoading)
                }
                if model.isLoading && !model.activities.isEmpty { ProgressView() }
            }.padding()
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle("Activities")
        .refreshable { await model.refresh() }
        .toolbar { Button { Task { await model.refresh() } } label: { Image(systemName: "arrow.clockwise") }.accessibilityLabel("Refresh activities").disabled(model.isLoading) }
    }
}

private struct ActivityCard: View {
    let activity: MobileActivity
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label(activity.typeLabel, systemImage: activity.type.lowercased().contains("ride") ? "bicycle" : "figure.run")
                Spacer()
                Text(activity.startTime.prefix(10))
            }.font(.caption).foregroundStyle(.secondary)
            Text(activity.name).font(.title3.bold()).fixedSize(horizontal: false, vertical: true)
            RoutePreview(activity: activity).frame(height: 164).clipShape(RoundedRectangle(cornerRadius: 14))
            HStack(alignment: .firstTextBaseline) {
                Text(activity.distance).font(.title2.bold())
                Spacer()
                Text(activity.duration).font(.headline).foregroundStyle(.secondary)
            }
            HStack {
                Label("\(activity.stopCount) stops", systemImage: "pause.circle")
                Spacer()
                Label("\(activity.detectedFactCount) facts", systemImage: "sparkles")
            }.font(.caption).foregroundStyle(.secondary)
        }
        .padding(16).background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 22))
    }
}

private struct RoutePreview: View {
    let activity: MobileActivity
    private var coordinates: [CLLocationCoordinate2D] {
        (activity.route ?? []).filter(\.isValid).map { CLLocationCoordinate2D(latitude: $0.lat, longitude: $0.lon) }
    }
    var body: some View {
        if coordinates.count > 1 {
            Map(interactionModes: []) {
                MapPolyline(coordinates: coordinates).stroke(.purple, style: StrokeStyle(lineWidth: 4, lineCap: .round, lineJoin: .round))
            }
            .mapStyle(.standard(elevation: .flat, pointsOfInterest: .excludingAll, showsTraffic: false))
            .accessibilityLabel("GPS route for \(activity.name)")
        } else {
            VStack(spacing: 8) {
                Image(systemName: activity.routeStatus == "error" ? "exclamationmark.triangle" : "map").font(.title2)
                Text(placeholder).font(.caption).multilineTextAlignment(.center)
            }.foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Color(.tertiarySystemFill))
        }
    }
    private var placeholder: String {
        if activity.routeStatus == "error" { return "Route could not be loaded. Pull to refresh." }
        if activity.routeStatus == nil { return "Route preview unavailable on this server." }
        if activity.type.lowercased().contains("virtual") { return "Virtual activity · no outdoor GPS route" }
        if let status = activity.gpsStatus, !status.isEmpty { return "No GPS route · \(status)" }
        return "No GPS route recorded"
    }
}

private struct YearView: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("weirdstats.selected-year") private var year = Calendar.current.component(.year, from: Date())
    @State private var selectedDay: HistoryDay?
    private func monthLabel(_ date: String) -> String {
        guard let number = Int(date.dropFirst(5).prefix(2)), (1...12).contains(number) else { return "" }
        return Calendar.current.shortMonthSymbols[number - 1]
    }
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                Text("Your year in motion").font(.title2.bold())
                Text("The same effort calendar as WeirdStats on the web. Every square is one day, including activities without GPS.")
                    .font(.subheadline).foregroundStyle(.secondary)
                Picker("Year", selection: $year) {
                    ForEach(Array(Set(model.years + [year])).sorted(by: >), id: \.self) { Text(String($0)).tag($0) }
                }.pickerStyle(.menu)
                if model.isLoadingHistory { ProgressView("Loading year…").frame(maxWidth: .infinity).padding() }
                if !model.historyError.isEmpty { ErrorNotice(message: model.historyError) { await model.loadHistory(year: year) } }
                if let history = model.history, history.year == year {
                    Text("\(history.totalEffort, specifier: "%.1f") effort hours").font(.title.bold())
                    HStack(alignment: .top, spacing: 8) {
                        VStack(spacing: 5) {
                            Color.clear.frame(height: 18)
                            ForEach(["M", "T", "W", "T", "F", "S", "S"].indices, id: \.self) { index in
                                Text(["M", "T", "W", "T", "F", "S", "S"][index]).font(.caption2).frame(width: 14, height: 22)
                            }
                        }.foregroundStyle(.secondary)
                        ScrollView(.horizontal) {
                            VStack(alignment: .leading, spacing: 5) {
                                HStack(spacing: 5) {
                                    ForEach(0..<((history.days.count + 6) / 7), id: \.self) { week in
                                        let dates = history.days.dropFirst(week * 7).prefix(7)
                                        Text(dates.first(where: { $0.date.hasSuffix("-01") }).map { monthLabel($0.date) } ?? "")
                                            .font(.system(size: 10)).frame(width: 22, height: 18, alignment: .leading)
                                    }
                                }.foregroundStyle(.secondary)
                                LazyHGrid(rows: Array(repeating: GridItem(.fixed(22), spacing: 5), count: 7), spacing: 5) {
                                    ForEach(history.days) { day in
                                        Button { selectedDay = day } label: {
                                            RoundedRectangle(cornerRadius: 4)
                                                .fill(day.inRange ? Color.purple.opacity(day.level == 0 ? 0.08 : min(1, 0.18 + Double(day.level) / 13)) : Color.clear)
                                                .frame(width: 22, height: 22)
                                                .overlay(RoundedRectangle(cornerRadius: 4).stroke(selectedDay?.id == day.id ? Color.primary : .clear, lineWidth: 2))
                                        }.buttonStyle(.plain).accessibilityLabel(day.tooltip)
                                    }
                                }
                            }
                        }
                    }
                    HStack { Text("Less"); ForEach(0..<6) { n in RoundedRectangle(cornerRadius: 3).fill(Color.purple.opacity(0.08 + Double(n) * 0.18)).frame(width: 14, height: 14) }; Text("More") }.font(.caption).foregroundStyle(.secondary)
                    if let selectedDay { Text(selectedDay.tooltip).font(.callout) }
                    Text("Swipe across to explore January–December. Tap a day for its effort. Effort uses the website’s normalized score, or moving hours when unavailable.")
                        .font(.caption).foregroundStyle(.secondary)
                } else if !model.isLoadingHistory && model.historyError.isEmpty {
                    ContentUnavailableView("No history loaded", systemImage: "calendar", description: Text("Refresh to load your activity year from the server."))
                }
            }.padding()
        }
        .navigationTitle("Year")
        .task(id: year) { selectedDay = nil; await model.loadHistory(year: year) }
        .refreshable { await model.loadHistory(year: year) }
    }
}

private struct FactsView: View {
    @EnvironmentObject private var model: AppModel
    var body: some View {
        List {
            Section { Text("Real discoveries from your imported activities. Load older activities to grow your collection.").font(.subheadline).foregroundStyle(.secondary) }
            if !model.errorMessage.isEmpty { ErrorNotice(message: model.errorMessage) { await model.refresh() } }
            if model.isLoading { ProgressView("Loading facts…") }
            if !model.isLoading && model.activities.allSatisfy({ ($0.facts ?? []).isEmpty }) {
                ContentUnavailableView("No collected facts yet", systemImage: "sparkles", description: Text("Only facts detected by the server appear here. Import or process activities in Profile."))
            }
            ForEach(model.activities.filter { !($0.facts ?? []).isEmpty }) { activity in
                Section(activity.name) {
                    ForEach(activity.facts ?? []) { fact in
                        NavigationLink { ActivityDetailView(activityID: activity.id, focusedFactID: fact.id) } label: {
                            VStack(alignment: .leading, spacing: 8) {
                                HStack { Text(fact.title).font(.headline); Spacer(); if let badge = fact.badgeLabel { Text(badge).font(.caption).foregroundStyle(.purple) } }
                                Text(fact.summary).font(.subheadline)
                                Text(activity.startTime.prefix(10)).font(.caption).foregroundStyle(.secondary)
                            }.padding(.vertical, 6)
                        }.accessibilityIdentifier("activity-fact-\(activity.id)-\(fact.id)")
                    }
                }
            }
            if model.activities.contains(where: { $0.factsStatus == "error" }) { Text("Some facts could not be loaded. Pull to refresh.").foregroundStyle(.red) }
            if model.activities.contains(where: { $0.factsStatus == "pending" }) { Text("Some activities are still waiting for fact processing. Check Profile for import status.").font(.caption).foregroundStyle(.secondary) }
            if model.activities.contains(where: { $0.factsStatus == nil }) { Text("This server does not provide collected facts yet. A backend update is required.").font(.caption).foregroundStyle(.secondary) }
            if model.nextCursor != nil { Button("Load older activities and facts") { Task { await model.loadMore() } }.disabled(model.isLoading) }
        }.navigationTitle("Facts").refreshable { await model.refresh() }
    }
}

private struct ProfileView: View {
    @EnvironmentObject private var model: AppModel
    var body: some View {
        List {
            Section("Account") {
                Label(model.athleteName.isEmpty ? "Your Strava account" : model.athleteName, systemImage: "person.crop.circle.fill")
                LabeledContent("Backend", value: model.serverURLText).font(.caption)
                Button("Refresh profile and activities") { Task { await model.refresh() } }.disabled(model.isLoading)
                if model.isLoading { ProgressView("Refreshing…") }
            }
            Section("Activity import") {
                if let sync = model.syncStatus {
                    Text(sync.message)
                    Text("\(sync.completed) processed · \(sync.pending) waiting").foregroundStyle(.secondary)
                    Text("\(sync.failed) failed · \(sync.blocked) blocked").font(.caption)
                    if let next = sync.nextRunAt { Text("Next attempt: \(Date(timeIntervalSince1970: TimeInterval(next)).formatted())").font(.caption) }
                    ForEach(sync.errors) { error in Text("\(error.message) Reference #\(error.jobID)").font(.caption) }
                    if sync.blocked > 0 { Button("Reconnect Strava") { Task { await model.signIn() } }.disabled(model.isAuthenticating) }
                    if sync.failed > 0 { Button("Retry failed activities") { Task { await model.retryActivities() } } }
                } else { Text("Refresh to check your import status.").foregroundStyle(.secondary) }
            }
            if !model.errorMessage.isEmpty { Section("Connection") { ErrorNotice(message: model.errorMessage) { await model.refresh() } } }
            Section { Button("Sign Out", role: .destructive) { model.signOut() } }
        }.navigationTitle("Profile").refreshable { await model.refresh() }
    }
}

private struct ErrorNotice: View {
    let message: String
    let retry: () async -> Void
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("Couldn’t refresh", systemImage: "exclamationmark.triangle").font(.headline)
            Text(message).font(.caption).fixedSize(horizontal: false, vertical: true)
            Button("Try again") { Task { await retry() } }
        }.foregroundStyle(.red).padding(.vertical, 8)
    }
}
