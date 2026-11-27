-- The day's set of instance IDs, kept only until the day is rolled up.
CREATE TABLE seen (
  day TEXT NOT NULL,
  version TEXT NOT NULL,
  instance_id TEXT NOT NULL,
  PRIMARY KEY (day, version, instance_id)
) WITHOUT ROWID;

-- The aggregate: distinct instances per version per UTC day.
CREATE TABLE daily_counts (
  day TEXT NOT NULL,
  version TEXT NOT NULL,
  instances INTEGER NOT NULL,
  PRIMARY KEY (day, version)
);
