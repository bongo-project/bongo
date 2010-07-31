CREATE TABLE cookies (
	guid			INTEGER NOT NULL UNIQUE,
	user			TEXT DEFAULT NULL,
	cookie			TEXT DEFAULT NULL,
	expiration		INTEGER DEFAULT 0
);

CREATE INDEX cookies_lookup ON cookies (user, cookie);

PRAGMA user_version = 1;
