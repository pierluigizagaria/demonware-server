# CTR Wumpa Challenges

The server evaluates the 59 daily, weekly and Pro definitions in Switch 1.0.15's
AchievementMetadata (`update.pak` entries 2028 and 6673). The checked-in
[`internal/ctr/ctr-challenge-rules.json`](internal/ctr/ctr-challenge-rules.json)
contains the original IDs, requirements, schedules and 25/75/200-coin rewards.
It repeats the client's 84-day cycle beginning March 26, 2020 at 15:00 UTC,
including the original weekly and six-week Pro windows.

The PS4 01.21 data matches these rules: `AchievementGrandPrixConfig.igz` in
`update2.pak`, which the game searches before `update.pak`, has identical
periods, targets, X/Y values and filters for all 59 rules, and the achievement
list in `AchievementEngineData.igz` gives the same IDs. The older copy in
`update.pak` differs and is not used. The 25/75/200-coin rewards are not in the
PS4 client data; they come from the Switch metadata.

Reports are matched by event name, track/character/kart/skin/power-up/cup filters
and X/Y requirements. String predicates use the client's seed-4 FNV hash.
Time-limit events compare seconds downward; drifting, ring counts and scores
compare upward. Different-battle victories count distinct modes 9–13.

Partial progress, completion and coins are saved together in the instance's
SQLite database. Completion survives reconnection and restart. Report receipts
suppress replays while preserving repeated occurrences inside one report.

Many gameplay events, including the longest consecutive drift, are submitted at
race completion. The server cannot award an event it has not received. Identical
events with no unique ID and the same second across separate reports cannot be
distinguished from retries; the server conservatively suppresses those repeats.

`go test ./...` covers original schedules, threshold direction, filters, weekly
progress, distinct modes, restart persistence and duplicate payouts. Not every
challenge has been verified in a live game session.
