CREATE TABLE cookies (
	id			INTEGER PRIMARY KEY AUTOINCREMENT,
	user			TEXT DEFAULT NULL,
	cookie			TEXT DEFAULT NULL,
	expiration		INTEGER DEFAULT 0
);

CREATE INDEX cookies_lookup ON cookies (user, cookie);
CREATE INDEX cookies_lookup_user ON cookies (user);
CREATE INDEX cookies_expiry ON cookies (expiration);

PRAGMA user_version = 1;
