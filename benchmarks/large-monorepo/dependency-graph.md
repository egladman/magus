# large-monorepo dependency graph

The full project dependency tree magus sees for the benchmark workspace: 5
Next.js apps (`apps/*`), each depending on its 20 feature libraries
(`packages/<app>/important-feature-*`); the `shared` packages and the root
are leaf inputs. `BR` is the affected blast-radius rank magus annotates;
`~1m` is the per-project build-time estimate.

Regenerate (after `./setup.sh`):

```sh
cd gen/repo && magus run build --graph -o mermaid
```

```mermaid
---
title: magus dependency graph (downstream)
---
graph TD
  subgraph spell_magusfile["magusfile"]
    _[".<br/>BR=1<br/>~1m"]
  end
  subgraph spell_ts["ts"]
    apps_crew["apps/crew<br/>BR=1<br/>~1m"]
    apps_flight_simulator["apps/flight-simulator<br/>BR=1<br/>~1m"]
    apps_navigation["apps/navigation<br/>BR=1<br/>~1m"]
    apps_ticket_booking["apps/ticket-booking<br/>BR=1<br/>~1m"]
    apps_warp_drive_manager["apps/warp-drive-manager<br/>BR=1<br/>~1m"]
    packages_crew_important_feature_0["packages/crew/important-feature-0<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_1["packages/crew/important-feature-1<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_10["packages/crew/important-feature-10<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_11["packages/crew/important-feature-11<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_12["packages/crew/important-feature-12<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_13["packages/crew/important-feature-13<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_14["packages/crew/important-feature-14<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_15["packages/crew/important-feature-15<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_16["packages/crew/important-feature-16<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_17["packages/crew/important-feature-17<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_18["packages/crew/important-feature-18<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_19["packages/crew/important-feature-19<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_2["packages/crew/important-feature-2<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_3["packages/crew/important-feature-3<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_4["packages/crew/important-feature-4<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_5["packages/crew/important-feature-5<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_6["packages/crew/important-feature-6<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_7["packages/crew/important-feature-7<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_8["packages/crew/important-feature-8<br/>BR=2<br/>~1m"]
    packages_crew_important_feature_9["packages/crew/important-feature-9<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_0["packages/flight-simulator/important-feature-0<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_1["packages/flight-simulator/important-feature-1<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_10["packages/flight-simulator/important-feature-10<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_11["packages/flight-simulator/important-feature-11<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_12["packages/flight-simulator/important-feature-12<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_13["packages/flight-simulator/important-feature-13<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_14["packages/flight-simulator/important-feature-14<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_15["packages/flight-simulator/important-feature-15<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_16["packages/flight-simulator/important-feature-16<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_17["packages/flight-simulator/important-feature-17<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_18["packages/flight-simulator/important-feature-18<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_19["packages/flight-simulator/important-feature-19<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_2["packages/flight-simulator/important-feature-2<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_3["packages/flight-simulator/important-feature-3<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_4["packages/flight-simulator/important-feature-4<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_5["packages/flight-simulator/important-feature-5<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_6["packages/flight-simulator/important-feature-6<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_7["packages/flight-simulator/important-feature-7<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_8["packages/flight-simulator/important-feature-8<br/>BR=2<br/>~1m"]
    packages_flight_simulator_important_feature_9["packages/flight-simulator/important-feature-9<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_0["packages/navigation/important-feature-0<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_1["packages/navigation/important-feature-1<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_10["packages/navigation/important-feature-10<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_11["packages/navigation/important-feature-11<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_12["packages/navigation/important-feature-12<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_13["packages/navigation/important-feature-13<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_14["packages/navigation/important-feature-14<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_15["packages/navigation/important-feature-15<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_16["packages/navigation/important-feature-16<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_17["packages/navigation/important-feature-17<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_18["packages/navigation/important-feature-18<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_19["packages/navigation/important-feature-19<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_2["packages/navigation/important-feature-2<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_3["packages/navigation/important-feature-3<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_4["packages/navigation/important-feature-4<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_5["packages/navigation/important-feature-5<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_6["packages/navigation/important-feature-6<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_7["packages/navigation/important-feature-7<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_8["packages/navigation/important-feature-8<br/>BR=2<br/>~1m"]
    packages_navigation_important_feature_9["packages/navigation/important-feature-9<br/>BR=2<br/>~1m"]
    packages_shared_alerts["packages/shared/alerts<br/>BR=1<br/>~1m"]
    packages_shared_buttons["packages/shared/buttons<br/>BR=1<br/>~1m"]
    packages_shared_components["packages/shared/components<br/>BR=1<br/>~1m"]
    packages_shared_dialogs["packages/shared/dialogs<br/>BR=1<br/>~1m"]
    packages_shared_icons["packages/shared/icons<br/>BR=1<br/>~1m"]
    packages_ticket_booking_important_feature_0["packages/ticket-booking/important-feature-0<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_1["packages/ticket-booking/important-feature-1<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_10["packages/ticket-booking/important-feature-10<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_11["packages/ticket-booking/important-feature-11<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_12["packages/ticket-booking/important-feature-12<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_13["packages/ticket-booking/important-feature-13<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_14["packages/ticket-booking/important-feature-14<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_15["packages/ticket-booking/important-feature-15<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_16["packages/ticket-booking/important-feature-16<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_17["packages/ticket-booking/important-feature-17<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_18["packages/ticket-booking/important-feature-18<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_19["packages/ticket-booking/important-feature-19<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_2["packages/ticket-booking/important-feature-2<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_3["packages/ticket-booking/important-feature-3<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_4["packages/ticket-booking/important-feature-4<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_5["packages/ticket-booking/important-feature-5<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_6["packages/ticket-booking/important-feature-6<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_7["packages/ticket-booking/important-feature-7<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_8["packages/ticket-booking/important-feature-8<br/>BR=2<br/>~1m"]
    packages_ticket_booking_important_feature_9["packages/ticket-booking/important-feature-9<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_0["packages/warp-drive-manager/important-feature-0<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_1["packages/warp-drive-manager/important-feature-1<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_10["packages/warp-drive-manager/important-feature-10<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_11["packages/warp-drive-manager/important-feature-11<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_12["packages/warp-drive-manager/important-feature-12<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_13["packages/warp-drive-manager/important-feature-13<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_14["packages/warp-drive-manager/important-feature-14<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_15["packages/warp-drive-manager/important-feature-15<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_16["packages/warp-drive-manager/important-feature-16<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_17["packages/warp-drive-manager/important-feature-17<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_18["packages/warp-drive-manager/important-feature-18<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_19["packages/warp-drive-manager/important-feature-19<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_2["packages/warp-drive-manager/important-feature-2<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_3["packages/warp-drive-manager/important-feature-3<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_4["packages/warp-drive-manager/important-feature-4<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_5["packages/warp-drive-manager/important-feature-5<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_6["packages/warp-drive-manager/important-feature-6<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_7["packages/warp-drive-manager/important-feature-7<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_8["packages/warp-drive-manager/important-feature-8<br/>BR=2<br/>~1m"]
    packages_warp_drive_manager_important_feature_9["packages/warp-drive-manager/important-feature-9<br/>BR=2<br/>~1m"]
  end

  apps_crew --> packages_crew_important_feature_0
  apps_crew --> packages_crew_important_feature_1
  apps_crew --> packages_crew_important_feature_10
  apps_crew --> packages_crew_important_feature_11
  apps_crew --> packages_crew_important_feature_12
  apps_crew --> packages_crew_important_feature_13
  apps_crew --> packages_crew_important_feature_14
  apps_crew --> packages_crew_important_feature_15
  apps_crew --> packages_crew_important_feature_16
  apps_crew --> packages_crew_important_feature_17
  apps_crew --> packages_crew_important_feature_18
  apps_crew --> packages_crew_important_feature_19
  apps_crew --> packages_crew_important_feature_2
  apps_crew --> packages_crew_important_feature_3
  apps_crew --> packages_crew_important_feature_4
  apps_crew --> packages_crew_important_feature_5
  apps_crew --> packages_crew_important_feature_6
  apps_crew --> packages_crew_important_feature_7
  apps_crew --> packages_crew_important_feature_8
  apps_crew --> packages_crew_important_feature_9
  apps_flight_simulator --> packages_flight_simulator_important_feature_0
  apps_flight_simulator --> packages_flight_simulator_important_feature_1
  apps_flight_simulator --> packages_flight_simulator_important_feature_10
  apps_flight_simulator --> packages_flight_simulator_important_feature_11
  apps_flight_simulator --> packages_flight_simulator_important_feature_12
  apps_flight_simulator --> packages_flight_simulator_important_feature_13
  apps_flight_simulator --> packages_flight_simulator_important_feature_14
  apps_flight_simulator --> packages_flight_simulator_important_feature_15
  apps_flight_simulator --> packages_flight_simulator_important_feature_16
  apps_flight_simulator --> packages_flight_simulator_important_feature_17
  apps_flight_simulator --> packages_flight_simulator_important_feature_18
  apps_flight_simulator --> packages_flight_simulator_important_feature_19
  apps_flight_simulator --> packages_flight_simulator_important_feature_2
  apps_flight_simulator --> packages_flight_simulator_important_feature_3
  apps_flight_simulator --> packages_flight_simulator_important_feature_4
  apps_flight_simulator --> packages_flight_simulator_important_feature_5
  apps_flight_simulator --> packages_flight_simulator_important_feature_6
  apps_flight_simulator --> packages_flight_simulator_important_feature_7
  apps_flight_simulator --> packages_flight_simulator_important_feature_8
  apps_flight_simulator --> packages_flight_simulator_important_feature_9
  apps_navigation --> packages_navigation_important_feature_0
  apps_navigation --> packages_navigation_important_feature_1
  apps_navigation --> packages_navigation_important_feature_10
  apps_navigation --> packages_navigation_important_feature_11
  apps_navigation --> packages_navigation_important_feature_12
  apps_navigation --> packages_navigation_important_feature_13
  apps_navigation --> packages_navigation_important_feature_14
  apps_navigation --> packages_navigation_important_feature_15
  apps_navigation --> packages_navigation_important_feature_16
  apps_navigation --> packages_navigation_important_feature_17
  apps_navigation --> packages_navigation_important_feature_18
  apps_navigation --> packages_navigation_important_feature_19
  apps_navigation --> packages_navigation_important_feature_2
  apps_navigation --> packages_navigation_important_feature_3
  apps_navigation --> packages_navigation_important_feature_4
  apps_navigation --> packages_navigation_important_feature_5
  apps_navigation --> packages_navigation_important_feature_6
  apps_navigation --> packages_navigation_important_feature_7
  apps_navigation --> packages_navigation_important_feature_8
  apps_navigation --> packages_navigation_important_feature_9
  apps_ticket_booking --> packages_ticket_booking_important_feature_0
  apps_ticket_booking --> packages_ticket_booking_important_feature_1
  apps_ticket_booking --> packages_ticket_booking_important_feature_10
  apps_ticket_booking --> packages_ticket_booking_important_feature_11
  apps_ticket_booking --> packages_ticket_booking_important_feature_12
  apps_ticket_booking --> packages_ticket_booking_important_feature_13
  apps_ticket_booking --> packages_ticket_booking_important_feature_14
  apps_ticket_booking --> packages_ticket_booking_important_feature_15
  apps_ticket_booking --> packages_ticket_booking_important_feature_16
  apps_ticket_booking --> packages_ticket_booking_important_feature_17
  apps_ticket_booking --> packages_ticket_booking_important_feature_18
  apps_ticket_booking --> packages_ticket_booking_important_feature_19
  apps_ticket_booking --> packages_ticket_booking_important_feature_2
  apps_ticket_booking --> packages_ticket_booking_important_feature_3
  apps_ticket_booking --> packages_ticket_booking_important_feature_4
  apps_ticket_booking --> packages_ticket_booking_important_feature_5
  apps_ticket_booking --> packages_ticket_booking_important_feature_6
  apps_ticket_booking --> packages_ticket_booking_important_feature_7
  apps_ticket_booking --> packages_ticket_booking_important_feature_8
  apps_ticket_booking --> packages_ticket_booking_important_feature_9
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_0
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_1
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_10
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_11
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_12
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_13
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_14
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_15
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_16
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_17
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_18
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_19
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_2
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_3
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_4
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_5
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_6
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_7
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_8
  apps_warp_drive_manager --> packages_warp_drive_manager_important_feature_9

  classDef spell_magusfile fill:#888888,color:#fff
  classDef spell_ts fill:#888888,color:#fff

  class _ spell_magusfile
  class apps_crew,apps_flight_simulator,apps_navigation,apps_ticket_booking,apps_warp_drive_manager,packages_crew_important_feature_0,packages_crew_important_feature_1,packages_crew_important_feature_10,packages_crew_important_feature_11,packages_crew_important_feature_12,packages_crew_important_feature_13,packages_crew_important_feature_14,packages_crew_important_feature_15,packages_crew_important_feature_16,packages_crew_important_feature_17,packages_crew_important_feature_18,packages_crew_important_feature_19,packages_crew_important_feature_2,packages_crew_important_feature_3,packages_crew_important_feature_4,packages_crew_important_feature_5,packages_crew_important_feature_6,packages_crew_important_feature_7,packages_crew_important_feature_8,packages_crew_important_feature_9,packages_flight_simulator_important_feature_0,packages_flight_simulator_important_feature_1,packages_flight_simulator_important_feature_10,packages_flight_simulator_important_feature_11,packages_flight_simulator_important_feature_12,packages_flight_simulator_important_feature_13,packages_flight_simulator_important_feature_14,packages_flight_simulator_important_feature_15,packages_flight_simulator_important_feature_16,packages_flight_simulator_important_feature_17,packages_flight_simulator_important_feature_18,packages_flight_simulator_important_feature_19,packages_flight_simulator_important_feature_2,packages_flight_simulator_important_feature_3,packages_flight_simulator_important_feature_4,packages_flight_simulator_important_feature_5,packages_flight_simulator_important_feature_6,packages_flight_simulator_important_feature_7,packages_flight_simulator_important_feature_8,packages_flight_simulator_important_feature_9,packages_navigation_important_feature_0,packages_navigation_important_feature_1,packages_navigation_important_feature_10,packages_navigation_important_feature_11,packages_navigation_important_feature_12,packages_navigation_important_feature_13,packages_navigation_important_feature_14,packages_navigation_important_feature_15,packages_navigation_important_feature_16,packages_navigation_important_feature_17,packages_navigation_important_feature_18,packages_navigation_important_feature_19,packages_navigation_important_feature_2,packages_navigation_important_feature_3,packages_navigation_important_feature_4,packages_navigation_important_feature_5,packages_navigation_important_feature_6,packages_navigation_important_feature_7,packages_navigation_important_feature_8,packages_navigation_important_feature_9,packages_shared_alerts,packages_shared_buttons,packages_shared_components,packages_shared_dialogs,packages_shared_icons,packages_ticket_booking_important_feature_0,packages_ticket_booking_important_feature_1,packages_ticket_booking_important_feature_10,packages_ticket_booking_important_feature_11,packages_ticket_booking_important_feature_12,packages_ticket_booking_important_feature_13,packages_ticket_booking_important_feature_14,packages_ticket_booking_important_feature_15,packages_ticket_booking_important_feature_16,packages_ticket_booking_important_feature_17,packages_ticket_booking_important_feature_18,packages_ticket_booking_important_feature_19,packages_ticket_booking_important_feature_2,packages_ticket_booking_important_feature_3,packages_ticket_booking_important_feature_4,packages_ticket_booking_important_feature_5,packages_ticket_booking_important_feature_6,packages_ticket_booking_important_feature_7,packages_ticket_booking_important_feature_8,packages_ticket_booking_important_feature_9,packages_warp_drive_manager_important_feature_0,packages_warp_drive_manager_important_feature_1,packages_warp_drive_manager_important_feature_10,packages_warp_drive_manager_important_feature_11,packages_warp_drive_manager_important_feature_12,packages_warp_drive_manager_important_feature_13,packages_warp_drive_manager_important_feature_14,packages_warp_drive_manager_important_feature_15,packages_warp_drive_manager_important_feature_16,packages_warp_drive_manager_important_feature_17,packages_warp_drive_manager_important_feature_18,packages_warp_drive_manager_important_feature_19,packages_warp_drive_manager_important_feature_2,packages_warp_drive_manager_important_feature_3,packages_warp_drive_manager_important_feature_4,packages_warp_drive_manager_important_feature_5,packages_warp_drive_manager_important_feature_6,packages_warp_drive_manager_important_feature_7,packages_warp_drive_manager_important_feature_8,packages_warp_drive_manager_important_feature_9 spell_ts

  click _ "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo" "."
  click apps_crew "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/apps/crew" "apps/crew"
  click apps_flight_simulator "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/apps/flight-simulator" "apps/flight-simulator"
  click apps_navigation "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/apps/navigation" "apps/navigation"
  click apps_ticket_booking "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/apps/ticket-booking" "apps/ticket-booking"
  click apps_warp_drive_manager "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/apps/warp-drive-manager" "apps/warp-drive-manager"
  click packages_crew_important_feature_0 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-0" "packages/crew/important-feature-0"
  click packages_crew_important_feature_1 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-1" "packages/crew/important-feature-1"
  click packages_crew_important_feature_10 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-10" "packages/crew/important-feature-10"
  click packages_crew_important_feature_11 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-11" "packages/crew/important-feature-11"
  click packages_crew_important_feature_12 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-12" "packages/crew/important-feature-12"
  click packages_crew_important_feature_13 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-13" "packages/crew/important-feature-13"
  click packages_crew_important_feature_14 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-14" "packages/crew/important-feature-14"
  click packages_crew_important_feature_15 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-15" "packages/crew/important-feature-15"
  click packages_crew_important_feature_16 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-16" "packages/crew/important-feature-16"
  click packages_crew_important_feature_17 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-17" "packages/crew/important-feature-17"
  click packages_crew_important_feature_18 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-18" "packages/crew/important-feature-18"
  click packages_crew_important_feature_19 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-19" "packages/crew/important-feature-19"
  click packages_crew_important_feature_2 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-2" "packages/crew/important-feature-2"
  click packages_crew_important_feature_3 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-3" "packages/crew/important-feature-3"
  click packages_crew_important_feature_4 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-4" "packages/crew/important-feature-4"
  click packages_crew_important_feature_5 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-5" "packages/crew/important-feature-5"
  click packages_crew_important_feature_6 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-6" "packages/crew/important-feature-6"
  click packages_crew_important_feature_7 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-7" "packages/crew/important-feature-7"
  click packages_crew_important_feature_8 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-8" "packages/crew/important-feature-8"
  click packages_crew_important_feature_9 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/crew/important-feature-9" "packages/crew/important-feature-9"
  click packages_flight_simulator_important_feature_0 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-0" "packages/flight-simulator/important-feature-0"
  click packages_flight_simulator_important_feature_1 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-1" "packages/flight-simulator/important-feature-1"
  click packages_flight_simulator_important_feature_10 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-10" "packages/flight-simulator/important-feature-10"
  click packages_flight_simulator_important_feature_11 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-11" "packages/flight-simulator/important-feature-11"
  click packages_flight_simulator_important_feature_12 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-12" "packages/flight-simulator/important-feature-12"
  click packages_flight_simulator_important_feature_13 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-13" "packages/flight-simulator/important-feature-13"
  click packages_flight_simulator_important_feature_14 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-14" "packages/flight-simulator/important-feature-14"
  click packages_flight_simulator_important_feature_15 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-15" "packages/flight-simulator/important-feature-15"
  click packages_flight_simulator_important_feature_16 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-16" "packages/flight-simulator/important-feature-16"
  click packages_flight_simulator_important_feature_17 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-17" "packages/flight-simulator/important-feature-17"
  click packages_flight_simulator_important_feature_18 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-18" "packages/flight-simulator/important-feature-18"
  click packages_flight_simulator_important_feature_19 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-19" "packages/flight-simulator/important-feature-19"
  click packages_flight_simulator_important_feature_2 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-2" "packages/flight-simulator/important-feature-2"
  click packages_flight_simulator_important_feature_3 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-3" "packages/flight-simulator/important-feature-3"
  click packages_flight_simulator_important_feature_4 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-4" "packages/flight-simulator/important-feature-4"
  click packages_flight_simulator_important_feature_5 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-5" "packages/flight-simulator/important-feature-5"
  click packages_flight_simulator_important_feature_6 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-6" "packages/flight-simulator/important-feature-6"
  click packages_flight_simulator_important_feature_7 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-7" "packages/flight-simulator/important-feature-7"
  click packages_flight_simulator_important_feature_8 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-8" "packages/flight-simulator/important-feature-8"
  click packages_flight_simulator_important_feature_9 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/flight-simulator/important-feature-9" "packages/flight-simulator/important-feature-9"
  click packages_navigation_important_feature_0 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-0" "packages/navigation/important-feature-0"
  click packages_navigation_important_feature_1 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-1" "packages/navigation/important-feature-1"
  click packages_navigation_important_feature_10 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-10" "packages/navigation/important-feature-10"
  click packages_navigation_important_feature_11 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-11" "packages/navigation/important-feature-11"
  click packages_navigation_important_feature_12 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-12" "packages/navigation/important-feature-12"
  click packages_navigation_important_feature_13 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-13" "packages/navigation/important-feature-13"
  click packages_navigation_important_feature_14 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-14" "packages/navigation/important-feature-14"
  click packages_navigation_important_feature_15 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-15" "packages/navigation/important-feature-15"
  click packages_navigation_important_feature_16 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-16" "packages/navigation/important-feature-16"
  click packages_navigation_important_feature_17 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-17" "packages/navigation/important-feature-17"
  click packages_navigation_important_feature_18 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-18" "packages/navigation/important-feature-18"
  click packages_navigation_important_feature_19 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-19" "packages/navigation/important-feature-19"
  click packages_navigation_important_feature_2 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-2" "packages/navigation/important-feature-2"
  click packages_navigation_important_feature_3 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-3" "packages/navigation/important-feature-3"
  click packages_navigation_important_feature_4 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-4" "packages/navigation/important-feature-4"
  click packages_navigation_important_feature_5 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-5" "packages/navigation/important-feature-5"
  click packages_navigation_important_feature_6 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-6" "packages/navigation/important-feature-6"
  click packages_navigation_important_feature_7 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-7" "packages/navigation/important-feature-7"
  click packages_navigation_important_feature_8 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-8" "packages/navigation/important-feature-8"
  click packages_navigation_important_feature_9 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/navigation/important-feature-9" "packages/navigation/important-feature-9"
  click packages_shared_alerts "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/shared/alerts" "packages/shared/alerts"
  click packages_shared_buttons "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/shared/buttons" "packages/shared/buttons"
  click packages_shared_components "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/shared/components" "packages/shared/components"
  click packages_shared_dialogs "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/shared/dialogs" "packages/shared/dialogs"
  click packages_shared_icons "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/shared/icons" "packages/shared/icons"
  click packages_ticket_booking_important_feature_0 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-0" "packages/ticket-booking/important-feature-0"
  click packages_ticket_booking_important_feature_1 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-1" "packages/ticket-booking/important-feature-1"
  click packages_ticket_booking_important_feature_10 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-10" "packages/ticket-booking/important-feature-10"
  click packages_ticket_booking_important_feature_11 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-11" "packages/ticket-booking/important-feature-11"
  click packages_ticket_booking_important_feature_12 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-12" "packages/ticket-booking/important-feature-12"
  click packages_ticket_booking_important_feature_13 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-13" "packages/ticket-booking/important-feature-13"
  click packages_ticket_booking_important_feature_14 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-14" "packages/ticket-booking/important-feature-14"
  click packages_ticket_booking_important_feature_15 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-15" "packages/ticket-booking/important-feature-15"
  click packages_ticket_booking_important_feature_16 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-16" "packages/ticket-booking/important-feature-16"
  click packages_ticket_booking_important_feature_17 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-17" "packages/ticket-booking/important-feature-17"
  click packages_ticket_booking_important_feature_18 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-18" "packages/ticket-booking/important-feature-18"
  click packages_ticket_booking_important_feature_19 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-19" "packages/ticket-booking/important-feature-19"
  click packages_ticket_booking_important_feature_2 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-2" "packages/ticket-booking/important-feature-2"
  click packages_ticket_booking_important_feature_3 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-3" "packages/ticket-booking/important-feature-3"
  click packages_ticket_booking_important_feature_4 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-4" "packages/ticket-booking/important-feature-4"
  click packages_ticket_booking_important_feature_5 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-5" "packages/ticket-booking/important-feature-5"
  click packages_ticket_booking_important_feature_6 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-6" "packages/ticket-booking/important-feature-6"
  click packages_ticket_booking_important_feature_7 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-7" "packages/ticket-booking/important-feature-7"
  click packages_ticket_booking_important_feature_8 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-8" "packages/ticket-booking/important-feature-8"
  click packages_ticket_booking_important_feature_9 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/ticket-booking/important-feature-9" "packages/ticket-booking/important-feature-9"
  click packages_warp_drive_manager_important_feature_0 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-0" "packages/warp-drive-manager/important-feature-0"
  click packages_warp_drive_manager_important_feature_1 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-1" "packages/warp-drive-manager/important-feature-1"
  click packages_warp_drive_manager_important_feature_10 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-10" "packages/warp-drive-manager/important-feature-10"
  click packages_warp_drive_manager_important_feature_11 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-11" "packages/warp-drive-manager/important-feature-11"
  click packages_warp_drive_manager_important_feature_12 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-12" "packages/warp-drive-manager/important-feature-12"
  click packages_warp_drive_manager_important_feature_13 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-13" "packages/warp-drive-manager/important-feature-13"
  click packages_warp_drive_manager_important_feature_14 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-14" "packages/warp-drive-manager/important-feature-14"
  click packages_warp_drive_manager_important_feature_15 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-15" "packages/warp-drive-manager/important-feature-15"
  click packages_warp_drive_manager_important_feature_16 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-16" "packages/warp-drive-manager/important-feature-16"
  click packages_warp_drive_manager_important_feature_17 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-17" "packages/warp-drive-manager/important-feature-17"
  click packages_warp_drive_manager_important_feature_18 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-18" "packages/warp-drive-manager/important-feature-18"
  click packages_warp_drive_manager_important_feature_19 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-19" "packages/warp-drive-manager/important-feature-19"
  click packages_warp_drive_manager_important_feature_2 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-2" "packages/warp-drive-manager/important-feature-2"
  click packages_warp_drive_manager_important_feature_3 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-3" "packages/warp-drive-manager/important-feature-3"
  click packages_warp_drive_manager_important_feature_4 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-4" "packages/warp-drive-manager/important-feature-4"
  click packages_warp_drive_manager_important_feature_5 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-5" "packages/warp-drive-manager/important-feature-5"
  click packages_warp_drive_manager_important_feature_6 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-6" "packages/warp-drive-manager/important-feature-6"
  click packages_warp_drive_manager_important_feature_7 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-7" "packages/warp-drive-manager/important-feature-7"
  click packages_warp_drive_manager_important_feature_8 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-8" "packages/warp-drive-manager/important-feature-8"
  click packages_warp_drive_manager_important_feature_9 "file:///home/user/tack/magus/benchmarks/large-monorepo/gen/repo/packages/warp-drive-manager/important-feature-9" "packages/warp-drive-manager/important-feature-9"
```
