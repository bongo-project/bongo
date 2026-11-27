package main

// Both tables hold only a UTC calendar day, a version and counts. Nothing
// about the client is stored beyond the random instance ID.
const (
	// createSeen is the day's set of instance IDs, kept until the day is
	// rolled up.
	createSeen = `CREATE TABLE IF NOT EXISTS seen (
  day DATE NOT NULL,
  version TEXT NOT NULL,
  instance_id TEXT NOT NULL,
  PRIMARY KEY (day, version, instance_id)
)`

	// createDailyCounts is the aggregate: distinct instances per version per
	// UTC day.
	createDailyCounts = `CREATE TABLE IF NOT EXISTS daily_counts (
  day DATE NOT NULL,
  version TEXT NOT NULL,
  instances BIGINT NOT NULL,
  PRIMARY KEY (day, version)
)`

	insertSeen = `INSERT INTO seen (day, version, instance_id) VALUES ($1::date, $2, $3) ON CONFLICT DO NOTHING`

	// rollupSQL moves every day before $1 into daily_counts. DELETE ...
	// RETURNING locks the rows, so two overlapping runs count each row once.
	// The upsert is additive: a late row for a day already rolled up adds to
	// that day's count.
	rollupSQL = `WITH gone AS (DELETE FROM seen WHERE day < $1::date RETURNING day, version)
INSERT INTO daily_counts (day, version, instances)
SELECT day, version, count(*) FROM gone GROUP BY day, version
ON CONFLICT (day, version) DO UPDATE SET instances = daily_counts.instances + EXCLUDED.instances`

	retentionSQL = `DELETE FROM daily_counts WHERE day < $1::date`
)
