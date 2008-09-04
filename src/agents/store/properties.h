#ifndef PROPERTIES_H
#define PROPERTIES_H

// important - DB compatibility
// new properties must be defined after old ones, not between where
// that would cause them to become re-ordered.
// changes here must be reflected in StorePropertyDBLocation()
typedef enum {
	STORE_PROP_BADPROP = -1,
	STORE_PROP_NONE = 0,
	STORE_PROP_ALL = 1,
	
	STORE_PROP_ACCESS_CONTROL = 50,

	// properties on storeobject table
	STORE_PROP_COLLECTION = 100,
	STORE_PROP_CREATED,
	STORE_PROP_FLAGS,
	STORE_PROP_GUID,
	STORE_PROP_INDEX,
	STORE_PROP_LASTMODIFIED,
	STORE_PROP_LENGTH,
	STORE_PROP_TYPE,
	STORE_PROP_VERSION,
	STORE_PROP_NAME,
	STORE_PROP_MAIL_IMAPUID,	// hack - but this is internal now

	// properties on maildocument table
	STORE_PROP_MAIL_CONVERSATION = 150,
	STORE_PROP_MAIL_HEADERLEN,
	STORE_PROP_MAIL_MESSAGEID,
	STORE_PROP_MAIL_PARENTMESSAGEID,
	STORE_PROP_MAIL_SENT,
	STORE_PROP_MAIL_SUBJECT,
	STORE_PROP_MAIL_SENDERS,
	STORE_PROP_MAIL_LISTID,
	STORE_PROP_MAIL_TIMESENT,
	STORE_PROP_MAIL_MIMELINES,

	// properties on conversation table
	STORE_PROP_CONVERSATION_COUNT = 200,
	STORE_PROP_CONVERSATION_DATE,
	STORE_PROP_CONVERSATION_SUBJECT,
	STORE_PROP_CONVERSATION_UNREAD,
	STORE_PROP_CONVERSATION_SOURCES,

	// external properties
	STORE_PROP_EVENT_ALARM = 250,         /* another external property */
	STORE_PROP_EVENT_CALENDARS,
	STORE_PROP_EVENT_END,
	STORE_PROP_EVENT_LOCATION,
	STORE_PROP_EVENT_START,
	STORE_PROP_EVENT_SUMMARY,
	STORE_PROP_EVENT_UID,
	STORE_PROP_EVENT_STAMP,

	STORE_PROP_EXTERNAL = 4096
} StorePropertyType;

#define STORE_IS_EXTERNAL_PROPERTY_TYPE(_proptype) (49 < _proptype)

typedef enum {
	STORE_PROPTABLE_NONE = 0,
	STORE_PROPTABLE_SO = 1,
	STORE_PROPTABLE_CONV = 2,
	STORE_PROPTABLE_MAIL = 3,
	STORE_PROPTABLE_EVENT = 4
} StorePropertyTable;

typedef struct {
	StorePropertyType type;
	
	// location in database
	StorePropertyTable table;
	char const *table_name;
	char const *column;
	
	// name and content
	char *name;
	char *value;
	int valueLen;

	// querybuilder bits 
	BOOL output;
	int index;
} StorePropInfo;

typedef struct {
	const int intval;
	char const *name;
	const StorePropertyTable db_table;
	char const *column;
} StorePropValName;

int StorePropertyByName(char *name, StorePropInfo *prop);
int StorePropertyFixup(StorePropInfo *prop);

#endif
