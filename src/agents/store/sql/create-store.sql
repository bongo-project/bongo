CREATE TABLE storeobject (
	guid 			INTEGER PRIMARY KEY AUTOINCREMENT, 
	collection_guid 	INTEGER DEFAULT 0,
	imap_uid 		INTEGER DEFAULT 0,
	filename 		TEXT DEFAULT NULL, 
	type 			INTEGER DEFAULT 1,
	flags			INTEGER DEFAULT 0,
	size			INTEGER DEFAULT 0,
	time_created		INTEGER DEFAULT 0,
	time_modified		INTEGER DEFAULT 0
);

CREATE UNIQUE INDEX storeobject_idx ON storeobject (guid);
CREATE UNIQUE INDEX storeobject_filename ON storeobject (filename);
CREATE INDEX storeobject_type ON storeobject (type);
CREATE INDEX storeobject_imap_uid ON storeobject (imap_uid);

CREATE TABLE links (
	doc_guid		INTEGER NOT NULL,
	related_guid	INTEGER NOT NULL
);

CREATE INDEX links_doc ON links (doc_guid);
CREATE INDEX links_rel ON links (related_guid);

CREATE TABLE accesscontrols (
	guid			INTEGER NOT NULL,
	principal		INTEGER NOT NULL DEFAULT 0,
	priv			INTEGER NOT NULL DEFAULT 0,
	deny			INTEGER NOT NULL DEFAULT 1,
	who			TEXT NOT NULL
);

CREATE INDEX accesscontrols_guid ON accesscontrols (guid);

CREATE TABLE properties (
	guid			INTEGER NOT NULL,
	intprop			INTEGER DEFAULT 0,
	name			TEXT,
	value			TEXT NOT NULL
);

CREATE INDEX properties_guid ON properties (guid);
CREATE INDEX properties_intaccess ON properties (guid, intprop);
CREATE INDEX properties_nameaccess ON properties (guid, name);

CREATE TABLE propnames (
	id			INTEGER PRIMARY KEY AUTOINCREMENT,
	name			TEXT NOT NULL
);
CREATE INDEX propnames_id ON propnames (id);

CREATE TABLE eventdocument (
	guid			INTEGER NOT NULL,
	uid			TEXT DEFAULT NULL,
	summary			TEXT DEFAULT NULL,
	location		TEXT DEFAULT NULL,
	stamp			TEXT DEFAULT NULL,
	start			INTEGER DEFAULT 0,
	end			INTEGER DEFAULT 0
);

CREATE INDEX eventdocument_guid ON eventdocument (guid);
CREATE INDEX eventdocument_start ON eventdocument (start);
CREATE INDEX eventdocument_end ON eventdocument (end);


CREATE TABLE maildocument (
	guid			INTEGER NOT NULL,
	conversation_guid 	INTEGER DEFAULT 0,
	subject			TEXT DEFAULT NULL,
	senders			TEXT DEFAULT NULL,
	messageid		TEXT DEFAULT NULL,
	parent_messageid	TEXT DEFAULT NULL,
	listid			TEXT DEFAULT NULL,
	time_sent		INTEGER DEFAULT 0,
	mime_lines		INTEGER DEFAULT 0
);

CREATE INDEX maildocument_guid ON maildocument (guid);
CREATE INDEX maildocument_conversation_guid ON maildocument (conversation_guid);


CREATE TABLE conversation (
	guid			INTEGER NOT NULL UNIQUE,
	subject			TEXT DEFAULT NULL,
	date			INTEGER DEFAULT 0,
	sources			INTEGER DEFAULT 0,
	unread			INTEGER DEFAULT 0
);

CREATE INDEX conversation_guid ON conversation (guid);
CREATE INDEX conversation_date ON conversation (date);

PRAGMA user_version = 0;
