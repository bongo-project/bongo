/* Create stuff */

#include <xpl.h>
#include <memmgr.h>
#include <msgapi.h>

#include "properties.h"
#include "object-model.h"
#include "query-builder.h"
#include "command-parsing.h"
#include "messages.h"

extern const char *sql_create_store[];	// defined in create-store.s
extern const StorePropValName StorePropTable[]; // defined in properties.c

int	ACLCheckOnGUID(StoreClient *client, uint64_t guid, int prop);

/**
 * Create the initial internal layout in a new Store
 * \param	client	storeclient we're using
 * \return	0 on success, error codes otherwise
 */
int
StoreObjectDBCreate(StoreClient *client)
{
	int result;
	StoreObject collection; 
	
	result = MsgSQLBeginTransaction(client->storedb);
	if (result != 0) return result;
	
	result = MsgSQLQuickExecute(client->storedb, (const char*)sql_create_store);
	
	if (result != 0)
		return MsgSQLAbortTransaction(client->storedb);
	
	if (MsgSQLCommitTransaction(client->storedb)) {
		printf("Can't commit schema\n");
		MsgSQLAbortTransaction(client->storedb);
	} else {
		// we've created the new database. This means the schema has 
		// changed, and any compiled statements need to be cleared
		// away - otherwise odd errors (call out of sequence) will occur
		MsgSQLReset(client->storedb);
	}
	
	// now, create the default layout of the store.
	memset(&collection, 0, sizeof(StoreObject));
	collection.guid = STORE_ROOT_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/");
	collection.type = 4096;
	collection.time_created = time(NULL);
	collection.time_modified = time(NULL);
	result = StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_ADDRESSBOOK_GUID;
	collection.collection_guid = STORE_ROOT_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/addressbook");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_CONVERSATIONS_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/conversations");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_CALENDARS_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/calendars");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_MAIL_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/mail");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_PREFERENCES_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/preferences");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_EVENTS_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/events");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_MAIL_INBOX_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/mail/INBOX");
	collection.collection_guid = STORE_MAIL_GUID;
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_MAIL_DRAFTS_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/mail/drafts");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_MAIL_ARCHIVES_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/mail/archives");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_ADDRESSBOOK_PERSONAL_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/addressbook/personal");
	collection.collection_guid = STORE_ADDRESSBOOK_GUID;
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_ADDRESSBOOK_COLLECTED_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/addressbook/collected");
	StoreObjectCreate(client, &collection);
	
	collection.guid = STORE_CALENDARS_PERSONAL_GUID;
	snprintf(collection.filename, MAX_FILE_NAME, "/calendars/personal");
	StoreObjectCreate(client, &collection);
	
	return 0;
}

void
StorePropFixupNames(StorePropInfo *props, int propcount)
{
	// process props to put in names where internal properties are referenced
	int i;
	
	for (i = 0; i < propcount; i++) {
		StorePropertyFixup((StorePropInfo *) &props[i]);
	}
}

// fix up the filename in object based on its container 
void
StoreObjectFixUpFilename(StoreObject *container, StoreObject *object)
{
	// filename must be unique in the store!
	if (object->filename[0] == '\0') {
		// create a temp name if we weren't given one
		snprintf(object->filename, MAX_FILE_NAME, "%s/bongo-" GUID_FMT, container->filename, object->guid);
	} else {
		// need to prepand the filename of the parent collection
		// FIXME - should collections end with '/' ?
		char new_name[MAX_FILE_NAME];
		char *format = "%s/%s";
		char *filepart;
		
		filepart = strrchr(object->filename, '/');
		if (filepart == NULL)
			filepart = object->filename;
		else
			filepart++;
		
		// don't include separating / when prepending '/'
		if (container->filename[1] == '\0')
			format = "%s%s";
		
		snprintf(new_name, MAX_FILE_NAME, format, container->filename, filepart);
		strncpy(object->filename, new_name, MAX_FILE_NAME);
	}
}

/**
 * Create a new object in the store, with a given guid if possible
 * 
 * \param	client	Store client we're operating for
 * \param	object	Where to put the information.
 * \return	0 on success. -1 is 'guid already exists', -2 other failure.
 */
int
StoreObjectCreate(StoreClient *client, StoreObject *object)
{
	StoreObject root_collection;
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status;
	int retcode = -2;
	
	// FIXME: lock the non-existant entry
	if (StoreObjectFind(client, object->collection_guid, &root_collection)) {
		if (object->collection_guid > 0) {
			Log(LOG_ERROR, "Store '%s' has corrupt database", client->store);
			return -2;
		} 
		// no error if there is no parent (e.g., creating the root collection)
	}
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	// create a stub entry in the database to get our guid
	// use collection_guid = -1 to indicate doc doesn't exist yet.
	MsgSQLBeginTransaction(client->storedb);
	
	if (object->guid > 0) {
		query = "INSERT INTO storeobject (guid, collection_guid) VALUES (?1, -1);";
	} else {
		query = "INSERT INTO storeobject (collection_guid) VALUES (-1);";
	}
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	if (object->guid > 0) {
		MsgSQLBindInt64(&stmt, 1, object->guid);
	}
	object->time_created = time(NULL);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0) {
		// probably a duplicate object
		retcode = -1;
		goto abort;
	} else {
		object->guid = MsgSQLLastRowID(client->storedb);
	}
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;

	if (object->filename[0] != '/')
		// assume we need to fix the filename if it's not rooted.
		StoreObjectFixUpFilename(&root_collection, object);

	// create any files etc. needed
	if (STORE_IS_FOLDER(object->type)) {
		status = MaildirNew(client->store, object->guid);
		if (status < 0) {
			// couldn't create store
			StoreObjectRemove(client, object);
			goto abort;
		}
	}
	
	// save the new object, which updates the bad collection_guid
	status = StoreObjectSave(client, object);
	
	// FIXME: unlock the entry
	return 0;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return retcode;
}

/**
 * Find an object in the store by its guid
 * 
 * \param 	client	Store client we're operating for
 * \param	guid	GUID of the object we're looking for
 * \param	object	Where to put the object
 * \return	0 on success, -1 is 'no such guid', -2 is other failure
 */

#define storeobj_cols "so.guid, so.collection_guid, so.imap_uid, so.filename, so.type, so.flags, so.size, so.time_created, so.time_modified"
static void
StoreObjectFindQueryToObject(StoreObject *object, MsgSQLStatement *find)
{
	object->guid = MsgSQLResultInt64(find, 0);
	object->collection_guid = MsgSQLResultInt64(find, 1);
	object->imap_uid = MsgSQLResultInt(find, 2);
	MsgSQLResultText(find, 3, object->filename, MAX_FILE_NAME-1);
	object->filename[MAX_FILE_NAME] = '\0';
	object->type = MsgSQLResultInt(find, 4);
	object->flags = MsgSQLResultInt(find, 5);
	object->size = MsgSQLResultInt64(find, 6);
	object->time_created = MsgSQLResultInt(find, 7);
	object->time_modified = MsgSQLResultInt(find, 8);
	object->version = 1; 	// TODO - we don't do this yet.
}

int 
StoreObjectFind(StoreClient *client, uint64_t guid, StoreObject *object)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *find = NULL;
	int result;
	
	if (MsgSQLBeginTransaction(client->storedb)) return -2;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	find = MsgSQLPrepare(client->storedb, "SELECT " storeobj_cols " FROM storeobject so WHERE so.guid = ?1 LIMIT 1;", &stmt);
	if (find == NULL) goto abort;
	
	MsgSQLBindInt64(find, 1, guid);
	
	result = MsgSQLResults(client->storedb, find);
	if (result < 0) goto abort;
	if (result == 0) goto nosuchdoc;
	
	StoreObjectFindQueryToObject(object, find);
	
	MsgSQLEndStatement(&stmt);
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb)) goto abort;
	
	return 0;

nosuchdoc:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -2;
}

int
StoreObjectFindByFilename(StoreClient *client, const char *filename, StoreObject *object)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *find = NULL;
	int result;
	int retcode = -2;
	
	if (MsgSQLBeginTransaction(client->storedb)) return -2;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	find = MsgSQLPrepare(client->storedb, "SELECT " storeobj_cols " FROM storeobject so WHERE so.filename = ?1 LIMIT 1;", &stmt);
	if (find == NULL) goto abort;
	
	MsgSQLBindString(find, 1, filename, FALSE);
	
	result = MsgSQLResults(client->storedb, find);
	if (result < 0) goto abort;
	
	if (result == 0) {
		retcode = -1;
	} else {
		StoreObjectFindQueryToObject(object, find);
		retcode = 0;
	}
	
	MsgSQLEndStatement(&stmt);
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb)) {
		goto abort;
	}
	
	return retcode;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return retcode;
}

/**
 * Find the conversation for a store document
 * 
 * \param	client	Client we're performing this for
 * \param	data	Conversation we're looking for (need subject and date)
 * \param	conversation	Store object that we find
 * \return	0 on success, -1 for failure.
 */
int
StoreObjectFindConversation(StoreClient *client, StoreConversationData *data,
	StoreObject *conversation)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *find = NULL;
	int result;
	
	if (MsgSQLBeginTransaction(client->storedb)) return -2;
	
	// FIXME: needs to take into account date.
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	find = MsgSQLPrepare(client->storedb, "SELECT " storeobj_cols " FROM storeobject so INNER JOIN conversation c ON so.guid=c.guid WHERE c.subject=?1 LIMIT 1;", &stmt);
	if (find == NULL) goto abort;
	
	MsgSQLBindString(find, 1, data->subject, FALSE);
	// MsgSQLBindInt64(find, 1, guid);
	
	result = MsgSQLResults(client->storedb, find);
	if (result < 0) goto abort;
	if (result == 0) goto nosuchdoc;
	
	StoreObjectFindQueryToObject(conversation, find);
	
	MsgSQLEndStatement(&stmt);
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb)) goto abort;
	
	return 0;

nosuchdoc:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -2;
}

/**
 * Update the "time last modified" property on the store object
 * \param 	object	Store Object whose property we want to update
 * \return	nothing.
 */
void
StoreObjectUpdateModifiedTime(StoreObject *object)
{
	// update the modification time on the object
	object->time_modified = time(NULL);
}

/**
 * Save an object back into the store.
 * 
 * \param 	client	Store client we're operating for
 * \param	object	Object we want to save
 * \return	0 on success, -1 for failure.
 */
int 
StoreObjectSave(StoreClient *client, StoreObject *object)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int retcode = -2;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	// create a stub entry in the database to get our guid
	MsgSQLBeginTransaction(client->storedb);
	
	query = "UPDATE storeobject SET collection_guid=?2,filename=?3,type=?4," \
		"flags=?5,size=?6,time_modified=?7,time_created=?8,imap_uid=?9 WHERE guid=?1;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, object->guid);
	MsgSQLBindInt64(&stmt, 2, object->collection_guid);
	MsgSQLBindString(&stmt, 3, object->filename, FALSE);
	MsgSQLBindInt(&stmt, 4, object->type);
	MsgSQLBindInt(&stmt, 5, object->flags);
	MsgSQLBindInt(&stmt, 6, object->size);
	MsgSQLBindInt(&stmt, 7, object->time_modified);
	MsgSQLBindInt(&stmt, 8, object->time_created);
	MsgSQLBindInt(&stmt, 9, object->imap_uid);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0) {
		// perhaps the guid doesn't exist...
		retcode = -1;
		goto abort;
	} else {
		// printf("Updated object GUID: " GUID_FMT "\n", object->guid);
	}
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;

	return 0;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return retcode;
}

/**
 * Remove an object from the store by its guid
 * 
 * \param	client 	Store client we're operating for
 * \param	guid	GUID of the object we want to remove
 * \return	0 on success, -1 is 'no such guid', -2 is other failure
 */
int
StoreObjectRemove(StoreClient *client, StoreObject *object)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int retcode = -2;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	// try to remove the file/whatever first
	if (STORE_IS_FOLDER(object->type)) {
		MaildirRemove(client->store, object->guid);
	} else {
		// FIXME
	}
	
	// create a stub entry in the database to get our guid
	MsgSQLBeginTransaction(client->storedb);
	
	query = "DELETE FROM storeobject WHERE guid=?1;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, object->guid);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0) {
		// perhaps the guid doesn't exist...
		retcode = -1;
		goto abort;
	}
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;

	return 0;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return retcode;
}

void
StoreOutputProperty(StoreClient *client, StorePropertyType type, const char *name, const char *value)
{
	if (value != NULL) {
		const char *out = value;
		int outlen;
		
		if ((type == STORE_PROP_CREATED) || (type == STORE_PROP_LASTMODIFIED)) {
			// need to turn an int into a date.
			struct tm tm;
			time_t tt;
			char *endp, outval[65];
			uint64_t intval;
			
			intval = strtol(value, &endp, 10);
			if (*endp) {
				ConnWriteF(client->conn, "3246 Date property corrupt: %s\r\n", name);
				return;
			}
			tt = intval;
			gmtime_r(&tt, &tm);
			strftime(outval, 64,  "%Y-%m-%d %H:%M:%SZ", &tm);
			out = outval;
		}
		
		outlen = strlen(out);
		ConnWriteF(client->conn, "2001 %s %d\r\n%s\r\n", name, outlen, out);
	} else {
		ConnWriteF(client->conn, "3245 Property not found: %s\r\n", name);
	}
}

void
StoreObjectIterQueryBuilderPropResult(StoreClient *client, 
	int properties, MsgSQLStatement *stmt, QueryBuilder *builder)
{
	int col_id, result_id;
	char *result;
	
	// start value below "col_id + 9" depends on how many columns
	// we pull back in our initial query; careful :)
	for (col_id = 0, result_id = 0; col_id < properties; col_id++) {
		const StorePropInfo const *prop = BongoArrayIndex(builder->properties, 
			StorePropInfo *, col_id);
		
		// ignore properties we don't want output; 
		if (prop->output == FALSE) continue;
		
		MsgSQLResultTextPtr(stmt, result_id + 9, &result);
		StoreOutputProperty(client, prop->type, prop->name, result);
		
		// we keep a separate counter because we skip non-output
		// properties in the loop
		result_id++;
	}
	return;
}

/**
 * Run the query we've created in the QueryBuilder
 */
int
StoreObjectIterQueryBuilder(StoreClient *client, QueryBuilder *builder, BOOL show_total)
{
	char *sql = NULL;
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	StoreObject object;
	int properties;
	unsigned int i;
	long int total = 0;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	if (QueryBuilderRun(builder)) {
		printf("Couldn't parse the queries?\n");
		goto abort;
	} else if (QueryBuilderCreateSQL(builder, &sql)) {
		printf("Couldn't create the SQL\n");
		goto abort;
	} else {
		// printf("SQL: %s\n", sql);
	}
	
	MsgSQLBeginTransaction(client->storedb);
	
	ret = MsgSQLPrepare(client->storedb, sql, &stmt);
	if (ret == NULL) goto abort;
	
	// bind in any variables we need
	for (i = 0; i < BongoArrayCount(builder->parameters); i++) {
		QueryBuilder_Param *p = BongoArrayIndex((builder->parameters), 
			QueryBuilder_Param *, i);
		
		switch (p->type) {
			case TYPE_INT:
				MsgSQLBindInt(&stmt, p->position, p->data.d_int);
				break;
			case TYPE_INT64:
				MsgSQLBindInt64(&stmt, p->position, p->data.d_int64);
				break;
			case TYPE_TEXT:
				MsgSQLBindString(&stmt, p->position, p->data.d_text, FALSE);
				break;
			default:
				goto abort;
		}
	}
	
	properties = BongoArrayCount(builder->properties);
	
	while ((status = MsgSQLResults(client->storedb, &stmt)) > 0) {
		int ccode;
		char *ptr, *filename;
		char buffer[2 * sizeof(object.filename) + 1];
		
		StoreObjectFindQueryToObject(&object, &stmt);
		
		filename = object.filename;
		for (ptr = buffer; *filename; ++filename, ++ptr) {
			if (isspace(*filename)) *ptr++ = '\\';
			*ptr = *filename;
		}
		*ptr = 0;
		
		// FIXME: do we need to check READ permission on this object?
		
		// output document info if wanted
		if (builder->output_mode == MODE_LIST) {
			ccode = ConnWriteF(client->conn, 
				"2001 " GUID_FMT " %d %d %08x %d " FMT_UINT64_DEC " %s\r\n", 
				object.guid, object.type, object.flags, 
				object.imap_uid, object.time_created, object.size, buffer);
		}
		if (builder->output_mode == MODE_COLLECTIONS) {
			ccode = ConnWriteF(client->conn,
				"2001 " GUID_FMT " %d %d %s\r\n",
				object.guid, object.type, object.flags, buffer);
		}
		
		// output requested properties if any
		if (properties > 0) {
			StoreObjectIterQueryBuilderPropResult(client, 
				properties, &stmt, builder);
		}
		total++;
	}
	if (status < 0) goto abort; // SQL failure
	
	MsgSQLFinalize(&stmt);
	
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	if (builder->output_mode != MODE_PROPGET) {
		// we don't output a final success code in PROPGET 
		if (show_total) {
			ConnWriteF(client->conn, "1000 %ld\r\n", total);
		} else {
			ConnWriteStr(client->conn, MSG1000OK);
		}
	}
	
	QueryBuilderFinish(builder);
	return 1000;

abort:
	MsgSQLAbortTransaction(client->storedb);
	QueryBuilderFinish(builder);
	
	ConnWriteStr(client->conn, MSG5005DBLIBERR);
	
	return -1;
}

/**
 * Iterate across the contents of a collection
 * \param	client	Store client we're operating for
 * \param	guid	GUID of the collection we want to look in
 * \param	iterator	Iterator configuration
 */
int
StoreObjectIterCollectionContents(StoreClient *client, StoreObject *collection, 
	int start, int end, uint32_t flagsmask, uint32_t flags,
	StorePropInfo *props, int propcount,
	const char *safe_query, const char *unsafe_query,
	BOOL show_total)
{
	QueryBuilder builder;
	char *query;
	char final_query[200];
	int i;
	
	if (flags > 0) {
		if (flagsmask > 0) {
			query = "& = nmap.collection ?1 = ?2 ~ nmap.flags ?3";
		} else {
			query = "& = nmap.collection ?1 = nmap.flags ?2";
		}
	} else {
		query = "= nmap.collection ?1";
	}
	
	if (safe_query != NULL) {
		snprintf(final_query, 199, "& %s %s", query, safe_query);
		final_query[199] = '\0';
		query = final_query;
	}
	
	// no error checking here.. bad... :)
	QueryBuilderStart(&builder);
	
	QueryBuilderSetOutputMode(&builder, MODE_LIST);
	
	QueryBuilderSetQuerySafe(&builder, query);
	if (unsafe_query != NULL) 
		QueryBuilderSetQueryUnsafe(&builder, unsafe_query);
	
	QueryBuilderAddParam(&builder, 1, TYPE_INT64, 0, collection->guid, NULL);
	if (flags > 0) {
		QueryBuilderAddParam(&builder, 2, TYPE_INT, flags, 0, NULL);
		if (flagsmask > 0) {
			QueryBuilderAddParam(&builder, 3, TYPE_INT, flagsmask, 0, NULL);
		}
	}
	
	// FIXME: need a better internal properties model so that we can
	// do less work when passing around property lists.
	for (i = 0; i < propcount; i++) {
		StorePropInfo *prop = props + i;
		
		QueryBuilderAddPropertyOutput(&builder, prop->name);
	}
	
	// Range is [start, end] - but SQL wants [start, items to return]
	end -= start;
	if ((start > -1) && (end > -1)) {
		QueryBuilderSetResultRange(&builder, start, end);
	}
	
	return StoreObjectIterQueryBuilder(client, &builder, show_total);
}

/**
 * Iterate across a list of all collections underneath this one
 */
int
StoreObjectIterSubcollections(StoreClient *client, StoreObject *container)
{
	QueryBuilder builder;
	char *query;
	int len;
	
	query = "& & = nmap.type 4096 ! nmap.guid ?3 = { nmap.name ?2 ?1";
	
	// no error checking here.. bad... :)
	QueryBuilderStart(&builder);
	
	QueryBuilderSetQuerySafe(&builder, query);
	QueryBuilderSetOutputMode(&builder, MODE_COLLECTIONS);
	
	len = strlen(container->filename);
	
	QueryBuilderAddParam(&builder, 1, TYPE_TEXT, 0, 0, container->filename);
	QueryBuilderAddParam(&builder, 2, TYPE_INT, len, 0, NULL);
	QueryBuilderAddParam(&builder, 3, TYPE_INT64, 0, container->guid, NULL);
	
	return StoreObjectIterQueryBuilder(client, &builder, FALSE);
}

/**
 * Iterate across the members of a conversation
 */
int
StoreObjectIterConversationMails(StoreClient *client, StoreObject *conversation,
	StorePropInfo *props, int propcount)
{
	QueryBuilder builder;
	char *query;
	int i;
	
	query = "& = nmap.type 2 l to ?1";
	
	// no error checking here.. bad... :)
	QueryBuilderStart(&builder);
	
	QueryBuilderSetQuerySafe(&builder, query);
	
	for (i = 0; i < propcount; i++) {
		StorePropInfo *prop = props + i;
		
		QueryBuilderAddPropertyOutput(&builder, prop->name);
	}
	QueryBuilderAddParam(&builder, 1, TYPE_INT64, 0, conversation->guid, NULL);
	
	return StoreObjectIterQueryBuilder(client, &builder, FALSE);
}

/**
 * Iterate across any documents linked to this one
 */
int
StoreObjectIterLinks(StoreClient *client, StoreObject *document, BOOL reverse)
{
	QueryBuilder builder;
	char *query;
	
	if (reverse)
		query = "l from ?1";
	else
		query = "l to ?1";
	
	QueryBuilderStart(&builder);
	
	QueryBuilderSetQuerySafe(&builder, query);
	
	QueryBuilderAddParam(&builder, 1, TYPE_INT64, 0, document->guid, NULL);
	
	return StoreObjectIterQueryBuilder(client, &builder, FALSE);
}

/**
 * Iterate across a single document. This is a bit heavyweight, but allows
 * us to bring to bear QueryBuilder's property SQL knowledge.
 * \param	client	the store client we're talking to
 * \param	document	the document we want to know about
 * \param	props	list of properties we want
 * \param	propcount	how many properties are in the list
 * \param	props_only	whether or not we're interested in props view
 * \return	success code
 */
int
StoreObjectIterDocinfo(StoreClient *client, StoreObject *document, 
	StorePropInfo *props, int propcount, BOOL props_only)
{
	QueryBuilder builder;
	char *query;
	int i;
	
	query = "= nmap.guid ?1";
	
	QueryBuilderStart(&builder);
	
	QueryBuilderSetQuerySafe(&builder, query);
	if (props_only)
		QueryBuilderSetOutputMode(&builder, MODE_PROPGET);
	else
		QueryBuilderSetOutputMode(&builder, MODE_LIST);
	
	for (i = 0; i < propcount; i++) {
		StorePropInfo *prop = props + i;
		QueryBuilderAddPropertyOutput(&builder, prop->name);
	}
	QueryBuilderAddParam(&builder, 1, TYPE_INT64, 0, document->guid, NULL);
	
	return StoreObjectIterQueryBuilder(client, &builder, FALSE);
}

int
StoreObjectIterProperties(StoreClient *client, StoreObject *document)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char intval[XPL_MAX_PATH+1];
	int ccode;
	
	// do 'internal' properties first
	snprintf(intval, XPL_MAX_PATH, GUID_FMT, document->guid);
	StoreOutputProperty(client, STORE_PROP_GUID, "nmap.guid", intval);
	
	snprintf(intval, XPL_MAX_PATH, GUID_FMT, document->collection_guid);
	StoreOutputProperty(client, STORE_PROP_COLLECTION, "nmap.collection", intval);
	
	snprintf(intval, XPL_MAX_PATH, "%d", document->type);
	StoreOutputProperty(client, STORE_PROP_TYPE, "nmap.type", intval);
	
	snprintf(intval, XPL_MAX_PATH, "%d", document->flags);
	StoreOutputProperty(client, STORE_PROP_FLAGS, "nmap.flags", intval);
	
	if ((document->type == STORE_DOCTYPE_MAIL) || (STORE_IS_FOLDER(document->type))) {
		snprintf(intval, XPL_MAX_PATH, "%08x", document->imap_uid);
		StoreOutputProperty(client, STORE_PROP_MAIL_IMAPUID, "nmap.mail.imapuid", intval);
	}
	
	snprintf(intval, XPL_MAX_PATH, FMT_UINT64_DEC, document->size);
	StoreOutputProperty(client, STORE_PROP_LENGTH, "nmap.length", intval);
	
	snprintf(intval, XPL_MAX_PATH, "%d", document->time_created);
	StoreOutputProperty(client, STORE_PROP_CREATED, "nmap.created", intval);
	
	snprintf(intval, XPL_MAX_PATH, "%d", document->time_modified);
	StoreOutputProperty(client, STORE_PROP_LASTMODIFIED, "nmap.lastmodified", intval);
	
	char *query = "SELECT intprop, name, value FROM properties WHERE guid = ?1;";
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret ==  NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, document->guid);
	
	while ((ccode = MsgSQLResults(client->storedb, &stmt)) > 0) {
		StorePropInfo prop;
		MemClear(&prop, sizeof(StorePropInfo));
		
		prop.type = MsgSQLResultInt(&stmt, 0);
		MsgSQLResultTextPtr(&stmt, 1, &prop.name);
		MsgSQLResultTextPtr(&stmt, 2, &prop.value);
		
		StorePropertyFixup(&prop);
		
		StoreOutputProperty(client, prop.type, prop.name, prop.value);
	}
	
	ConnWriteStr(client->conn, MSG1000OK);
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	ConnWriteStr(client->conn, MSG5005DBLIBERR);
	return -1;
}

// this API needs to be a bit better thought out
// we alter our parent collection without that being obvious. If we already have
// a store object which points to it and save that object, we've overwritten the
// correct value :(
int
StoreObjectUpdateImapUID(StoreClient *client, StoreObject *object)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	StoreObject collection;
	char *query;
	int new_imapuid;
	int status;
	
	if (StoreObjectFind(client, object->collection_guid, &collection)) {
		// couldn't find parent collection
		return -1;
	}
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	// update the imap uid in a transaction to assure atomicity
	new_imapuid = ++collection.imap_uid;
	query = "UPDATE storeobject SET imap_uid = ?1 WHERE guid IN (?2, ?3);";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt(&stmt, 1, new_imapuid);
	MsgSQLBindInt64(&stmt, 2, object->guid);
	MsgSQLBindInt64(&stmt, 3, collection.guid);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0) {
		// perhaps the guid doesn't exist...
		goto abort;
	}
	MsgSQLFinalize(&stmt);
	
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	// now we have a new imap uid, we can update our object
	object->imap_uid = new_imapuid;
	
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

// also dangerous :D
// call within transaction to copy existing properties.
int 
StoreObjectCopyInfo(StoreClient *client, StoreObject *old, StoreObject *newobj)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int ccode;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);

	query = "INSERT INTO properties (guid, intprop, name, value) SELECT ?2, intprop, name, value FROM properties WHERE guid = ?1;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, old->guid);
	MsgSQLBindInt64(&stmt, 2, newobj->guid);

	ccode = MsgSQLExecute(client->storedb, &stmt);
	if (ccode != 0) {
		// perhaps the guid doesn't exist...
		goto abort;
	}
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

// FIXME - this should be refactored like LIST was
int
StoreObjectRemoveCollection(StoreClient *client, StoreObject *collection)
{
	// FIXME: check removing /foo doesn't also remove /foobar
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	char path[MAX_FILE_NAME+1];
	uint64_t **guid_list = NULL;
	int guid_alloc, i, j, status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	query = "SELECT guid FROM storeobject WHERE substr(filename,1,?2) = ?1 ORDER BY length(filename) DESC;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	snprintf(path, MAX_FILE_NAME, "%s/", collection->filename);
	MsgSQLBindString(&stmt, 1, path, FALSE);
	MsgSQLBindInt(&stmt, 2, strlen(path));
	
	// allocate some memory to save the list of collections - we do this 
	// because an open transaction would conflict with StoreObjectFind
	// FIXME
	guid_alloc = 1000;
	guid_list = MemMalloc(sizeof(uint64_t) * guid_alloc);
	i = 0;
	
	while ((status = MsgSQLResults(client->storedb, &stmt)) > 0) {
		*(guid_list[i++]) = MsgSQLResultInt64(&stmt, 0);
		
		if (i >= guid_alloc) {
			guid_alloc += 1000;
			MemRealloc(guid_list, sizeof(uint64_t) * guid_alloc);
		}
	}
	if (status < 0) goto abort; // SQL failure
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;

	// remove each document and then collection
	for	(j = 0; j < i; j++) {
		StoreObject object;
		
		if (guid_list[j] > 0) {
			// don't include the container in the results...
			StoreObjectFind(client, *(guid_list[j]), &object);
			StoreObjectRemove(client, &object);
		}
	}

	MemFree(guid_list);
	
	// finally, remove the collection we were given
	StoreObjectRemove(client, collection);

	return 0;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	if (guid_list) MemFree(guid_list);
	
	return -1;
}

// update filenames held in objects 'below' container collection
int
StoreObjectRenameSubobjects(StoreClient *client, StoreObject *container, const char *new_root)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char path[MAX_FILE_NAME+1];
	char newpath[MAX_FILE_NAME+1];
	int pathlen;
	char *query;
	int status;
	
	if (! STORE_IS_FOLDER(container->type))
		return 0; // nothing to do
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	snprintf(path, MAX_FILE_NAME, "%s/", container->filename);
	snprintf(newpath, MAX_FILE_NAME, "%s/", new_root);
	
	MsgSQLBeginTransaction(client->storedb);
	
	// ?1 - path of container, ?2 - length of path ?1, ?3 should be ?2+1, ?4 - new path to set
	query = "UPDATE storeobject SET filename = ?4 || substr(filename,?3,-1) WHERE substr(filename,1,?2) = ?1;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	pathlen = strlen(path);
	MsgSQLBindString(&stmt, 1, path, FALSE);
	MsgSQLBindInt(&stmt, 2, pathlen);
	MsgSQLBindInt(&stmt, 3, pathlen+1);
	MsgSQLBindString(&stmt, 4, newpath, FALSE);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status < 0) goto abort; // SQL failure
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb)) goto abort;
	
	return 0;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

/* smaller functions to do 'common tasks' */

int
StoreObjectSetFilename(StoreObject *object, const StoreObject *collection, const char *filename)
{
	if (filename == NULL) {
		snprintf(object->filename, sizeof(object->filename), "%s/bongo-" GUID_FMT, 
			collection->filename, object->guid);
	} else {
		snprintf(object->filename, sizeof(object->filename), "%s/%s",
			collection->filename, filename);
	}
	return 0;
}

/**
 * Set a property on an object.
 * \param	client	Client we're working for
 * \param	object	Store object we want to put the property on
 * \param	prop	Store property we wish to set
 * \return	0 on success, -1 on error and client notified, < -1 otheriwse.
 */
int
StoreObjectSetProperty(StoreClient *client, StoreObject *object, StorePropInfo *prop)
{
	uint64_t timeval;
	int intval;
	
	switch (prop->type) {
		// can't set these types
		case STORE_PROP_BADPROP:
		case STORE_PROP_ALL:
		case STORE_PROP_NONE:
			ConnWriteStr(client->conn, MSG3244BADPROPNAME);
			return -1;
			
		// erk. obsolete types?
		case STORE_PROP_ACCESS_CONTROL:
		case STORE_PROP_INDEX:
			return -2;
		
		// can't set these types for consistency reasons
		case STORE_PROP_GUID:
		case STORE_PROP_COLLECTION:
		case STORE_PROP_LENGTH:
		case STORE_PROP_NAME:
			return -2;
		
		// time/date type properties
		case STORE_PROP_CREATED:
			if (ParseDateTimeToUint64(client, prop->value, &timeval) != TOKEN_OK) return -1;
			object->time_created = timeval;
			goto save_object;
		case STORE_PROP_LASTMODIFIED:
			if (ParseDateTimeToUint64(client, prop->value, &timeval) != TOKEN_OK) return -1;
			object->time_modified = timeval;
			goto save_object;
		
		// integer properties
		case STORE_PROP_FLAGS:
			if (ParseInt(client, prop->value, &intval) != TOKEN_OK) return -1;
			object->flags = intval;
			goto save_object;
		case STORE_PROP_TYPE:
			if (ParseInt(client, prop->value, &intval) != TOKEN_OK) return -1;
			object->type = intval;
			goto save_object;
		
		// hexadecimal properties
		case STORE_PROP_MAIL_IMAPUID:
			if (ParseHexU32(client, prop->value, &intval) != TOKEN_OK) return -1;
			object->imap_uid = intval;
			goto save_object;
		
		case STORE_PROP_EXTERNAL:
		default:
			// anything we don't code for specifically gets stored on the 
			// properties table
			return StoreObjectSetExternalProperty(client, object, prop);
	}
	
save_object:
	if (StoreObjectSave(client, object) == 0) {
		return 0;
	}
	return -2;
}

int
StoreObjectSetExternalProperty(StoreClient *client, StoreObject *object, StorePropInfo *prop)
{
	MsgSQLStatement insstmt;
	MsgSQLStatement *insret = NULL;
	MsgSQLStatement remstmt;
	MsgSQLStatement *remret = NULL;
	char *insquery;
	char *remquery;
	int ret;
	
	memset(&remstmt, 0, sizeof(MsgSQLStatement));
	memset(&insstmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	if (prop->type != STORE_PROP_EXTERNAL)
		remquery = "DELETE FROM properties WHERE guid = ?1 AND intprop = ?2;";
	else 
		remquery = "DELETE FROM properties WHERE guid = ?1 AND name = ?2;";
	
	insquery = "INSERT INTO properties (guid, intprop, name, value) VALUES (?1, ?2, ?3, ?4);";
	
	// remove old property first
	remret = MsgSQLPrepare(client->storedb, remquery, &remstmt);
	if (remret == NULL) goto abort;
	
	MsgSQLBindInt64(&remstmt, 1, object->guid);
	if (prop->type != STORE_PROP_EXTERNAL)
		MsgSQLBindInt(&remstmt, 2, (int)prop->type);
	else
		MsgSQLBindString(&remstmt, 2, prop->name, FALSE);
	
	ret = MsgSQLExecute(client->storedb, &remstmt);
	if (ret != 0) goto abort;
	
	// insert new property
	insret = MsgSQLPrepare(client->storedb, insquery, &insstmt);
	if (insret == NULL) goto abort;
	
	MsgSQLBindInt64(&insstmt, 1, object->guid);
	if (prop->type != STORE_PROP_EXTERNAL) {
		MsgSQLBindInt(&insstmt, 2, (int)prop->type);
		MsgSQLBindNull(&insstmt, 3);
	} else {
		MsgSQLBindNull(&insstmt, 2);
		MsgSQLBindString(&insstmt, 3, prop->name, TRUE);
	}
	MsgSQLBindString(&insstmt, 4, prop->value, FALSE);
	
	ret = MsgSQLExecute(client->storedb, &insstmt);
	if (ret != 0) goto abort;
	
	MsgSQLFinalize(&insstmt);
	MsgSQLFinalize(&remstmt);
	
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	return 0;
	
abort:
	if (insret) MsgSQLFinalize(&insstmt);
	if (remret) MsgSQLFinalize(&remstmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}


/**
 * Set an ACL on a Store object, whether it exists or not, or is duplicated.
 * \param	client	StoreClient we're working for
 * \param	object	Store object we want to remove the ACL from
 * \param	principal	Type of priv we want to remove, pass "STORE_PRINCIPAL_NONE" to remove all
 * \param	priv	Priviledge we want to remove
 * \param	who	Who we want to remove the priv from (leave NULL for all)
 * \param	deny	True if we want to DENY, False if we're setting a GRANT
 * \return	0 for success or errorcodes otherwise.
 */
// FIXME: should normalise existing ACLs in response; e.g., DENY ALL removes existing GRANT
int
StoreObjectAddACL(StoreClient *client, StoreObject *object, StorePrincipalType principal, StorePrivilege priv,
	BOOL deny, const char *who)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	query = "INSERT INTO accesscontrols (guid, priv, principal, deny, who) VALUES (?1, ?2, ?3, ?4, ?5);";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, object->guid);
	MsgSQLBindInt(&stmt, 2, priv);
	MsgSQLBindInt(&stmt, 3, principal);
	MsgSQLBindInt(&stmt, 4, deny? 1 : 0);
	MsgSQLBindString(&stmt, 5, who, FALSE);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0)
		goto abort;
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

/**
 * Remove an ACL from a Store object, whether it exists or not, or is duplicated.
 * \param	client	StoreClient we're working for
 * \param	object	Store object we want to remove the ACL from
 * \param	principal	Type of priv we want to remove, pass "STORE_PRINCIPAL_NONE" to remove all
 * \param	priv	Priviledge we want to remove
 * \param	who	Who we want to remove the priv from (leave NULL for all)
 * \return	0 for success or errorcodes otherwise.
 */
int
StoreObjectRemoveACL(StoreClient *client, StoreObject *object, StorePrincipalType principal, StorePrivilege priv, const char *who)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	BongoStringBuilder b;
	int status;
	int bound = 2;
	
	if (BongoStringBuilderInit(&b)) {
		// can't setup the query string
		return -1;
	}
	if (priv == 0) {
		// this is nonsense...
		return -2;
	}
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	//  AND priv=?2 AND type=?3 AND who=?4;
	BongoStringBuilderAppend(&b, "DELETE FROM accesscontrols WHERE guid=?1");
	if (principal != STORE_PRINCIPAL_NONE) {
		// "NONE" means "all" in this context :)
		BongoStringBuilderAppendF(&b, " AND principal=?%d", bound);
		bound++;
	}
	if (priv != STORE_PRIV_ALL) {
		BongoStringBuilderAppendF(&b, " AND priv=?%d", bound);
		bound++;
	}
	if (who != NULL) {
		BongoStringBuilderAppendF(&b, " AND who=?%d", bound);
		bound++;
	}
	BongoStringBuilderAppend(&b, ";");
	
	ret = MsgSQLPrepare(client->storedb, b.value, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, object->guid);
	bound = 2;
	if (principal != STORE_PRINCIPAL_NONE) {
		MsgSQLBindInt(&stmt, bound, principal);
		bound++;
	}
	if (priv != STORE_PRIV_ALL) {
		MsgSQLBindInt(&stmt, bound, priv);
		bound++;
	}
	if (who != NULL) {
		MsgSQLBindString(&stmt, bound, who, FALSE);
		bound++;
	}
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0)
		goto abort;
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	BongoStringBuilderDestroy(&b);
	return -1;
}

/**
 * List the ACLs on a particular store object
 * \param	client	StoreClient we're working for
 * \param	object	Store object we're interested in
 * \return	0 for success or errorcodes otherwise.
 */
int
StoreObjectListACL(StoreClient *client, StoreObject *object)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status, ccode;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	if (object != NULL) 
		query = "SELECT guid, priv, principal, deny, who FROM accesscontrols WHERE guid=?1;";
	else
		query = "SELECT guid, priv, principal, deny, who FROM accesscontrols;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	if (object != NULL) {
		MsgSQLBindInt64(&stmt, 1, object->guid);
	}
	
	while ((status = MsgSQLResults(client->storedb, &stmt)) > 0) {
		uint64_t guid;
		int priv, principal, deny;
		char const *priv_str, *principal_str, *deny_str;
		char *who;
		
		guid = MsgSQLResultInt64(&stmt, 0);
		priv = MsgSQLResultInt(&stmt, 1);
		principal = MsgSQLResultInt(&stmt, 2);
		deny = MsgSQLResultInt(&stmt, 3);
		MsgSQLResultTextPtr(&stmt, 4, &who);
		
		if (PrivilegeToString((const StorePrivilege) priv, &priv_str) != 0) {
			// bad privilege... 
			continue;
		}
		if (PrincipalToString((const StorePrincipalType) principal, &principal_str) != 0) {
			// bad principal...
			continue;
		}
		if (deny == 0) {
			deny_str = "GRANT";
		} else {
			deny_str = "DENY";
		}
		
		ccode = ConnWriteF(client->conn, "2001 " GUID_FMT " %s %s %s %s\r\n",
				guid, deny_str, priv_str, principal_str, who);
	}
	if (status < 0) goto abort; // SQL failure
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	ConnWriteStr(client->conn, MSG1000OK);
	return 0;
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

/**
 * See if this client is able to access a certain store object
 * 
 * \param	client	Store client we're operating for
 * \param	object	Store object we want to check if we're allowed to access
 * \return	0 if we're allowed, 1 if not. -1=db lib error, -2 corrupt acl, -3 other error
 */
int
StoreObjectCheckAuthorization(StoreClient *client, StoreObject *object, int prop)
{
	StoreObject checkobj;
	int current_result = 0;
	
	if (STORE_PRINCIPAL_USER == client->principal.type && 
	    (!strcmp(client->principal.name, client->storeName))) {
		// user always has full permissions to her own store.
		return 0;
	}
	if ((!strncmp(client->storeName, "_system", 7)) && IS_MANAGER(client)) {
		// agents have full permissions the system store. (might want to change this in future)
		return 0;
	}

	memcpy(&checkobj, object, sizeof(StoreObject));
	while (current_result == 0) {
		current_result = ACLCheckOnGUID(client, checkobj.guid, prop);
		
		if (current_result == 0) {
			// try to inherit from the containing collection so we can check that instead
			if (checkobj.collection_guid > 0) {
				StoreObjectFind(client, checkobj.collection_guid, &checkobj);
			} else {
				// default to deny if we have gone all the way up the tree
				current_result = 2;
			}
		}
	}
	return current_result - 1;
}

// returns: <0 error, 0 inherit, 1 allow, 2 deny

int
ACLCheckOnGUID(StoreClient *client, uint64_t guid, int prop)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status = 0;
	BOOL allow = FALSE;
	BOOL deny = FALSE;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	query = "SELECT priv, principal, deny, who FROM accesscontrols WHERE guid = ?1;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, guid);
	
	while ((status = MsgSQLResults(client->storedb, &stmt)) > 0) {
		int priv;
		int type;
		int deny;
		char *who;
		BOOL *grant;
		
		priv = MsgSQLResultInt(&stmt, 0);
		type = MsgSQLResultInt(&stmt, 1);
		deny = MsgSQLResultInt(&stmt, 2);
		MsgSQLResultTextPtr(&stmt, 3, &who);
		
		if (deny == 0) {
			grant = &allow;
		} else {
			grant = &deny;
		}
		
		if (priv & prop) {
			// this prop would apply, let's see if it matches
			switch(type) {
				case STORE_PRINCIPAL_ALL:
					// always applies
					*grant = TRUE;
					
					break;
				case STORE_PRINCIPAL_AUTHENTICATED:
					// FIXME
					break;
				case STORE_PRINCIPAL_USER:
					if (strcmp(who, client->principal.name) == 0) {
						*grant = TRUE;
					}
					break;
				case STORE_PRINCIPAL_GROUP:
					// FIXME
					break;
				case STORE_PRINCIPAL_TOKEN:
					// FIXME
					break;
				default:
					// do nothing
					break;
			}
		}
	}
	if (status < 0) goto abort; // SQL failure
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb)) goto abort;

	// we've been through all the properties
	if (deny == TRUE) return 2;
	if (allow == TRUE) return 1;
	
	// neither allowed nor denyed - inherit?
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

/**
 * Link two store objects together. Used with conversations, for example, where the
 * mails in a conversation are related to the conversation document. Relations are
 * one-way, so for two documents to relate to each other, they need to be linked
 * twice in both "directions".
 * 
 * \param	client	Store client we're operating for
 * \param	document	Document we want to relate to
 * \param	related		Relative we want to link to
 * \return	0 if successful, -1 otherwise
 */
int
StoreObjectLink(StoreClient *client, StoreObject *document, StoreObject *related)
{
	return StoreObjectLinkByGuids(client, document->guid, related->guid);
}

/**
 * Link two store objects together. See StoreObjectLink().
 * 
 * \param	client	Store client we're operating for
 * \param	document	GUID of document we want to relate to
 * \param	related		GUID of relative we want to link to
 * \return	0 if successful, -1 otherwise
 */
int
StoreObjectLinkByGuids(StoreClient *client, uint64_t document, uint64_t related)
{
	// FIXME: want to check that an existing link isn't in place somehow.
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	query = "INSERT INTO links (doc_guid, related_guid) VALUES (?1, ?2);";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, document);
	MsgSQLBindInt64(&stmt, 2, related);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0)
		goto abort;
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

/**
 * Unlink two store objects.
 * 
 * \param	client	Store client we're operating for
 * \param	document	Document we want to relate to
 * \param	related		Relative we want to unlink from
 * \return	0 if successful, -1 otherwise
 */
int
StoreObjectUnlink(StoreClient *client, StoreObject *document, StoreObject *related)
{
	return StoreObjectUnlinkByGuids(client, document->guid, related->guid);
}

/**
 * Unlink two store objects. See StoreObjectUnlink().
 * 
 * \param	client	Store client we're operating for
 * \param	document	GUID of document we want to relate to
 * \param	related		GUID of relative we want to unlink from
 * \return	0 if successful, -1 otherwise
 */
int
StoreObjectUnlinkByGuids(StoreClient *client, uint64_t document, uint64_t related)
{
	// FIXME: want to check that an existing link isn't in place somehow.
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	MsgSQLBeginTransaction(client->storedb);
	
	query = "DELETE FROM links  WHERE doc_guid=?1 AND related_guid=?2;";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, document);
	MsgSQLBindInt64(&stmt, 2, related);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0)
		goto abort;
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;
	
	return 0;
	
abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return -1;
}

/**
 * Save conversation data back into the store.
 * 
 * \param 	client	Store client we're operating for
 * \param	data	Conversation data we want to save
 * \return	0 on success, -1 for failure.
 */
int 
StoreObjectSaveConversation(StoreClient *client, StoreConversationData *data)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *ret;
	char *query;
	int retcode = -2;
	int status;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	
	// create a stub entry in the database to get our guid
	MsgSQLBeginTransaction(client->storedb);
	
	query = "INSERT OR REPLACE INTO conversation (guid, subject, date, sources) VALUES (?1, ?2, ?3, ?4);";
	
	ret = MsgSQLPrepare(client->storedb, query, &stmt);
	if (ret == NULL) goto abort;
	
	MsgSQLBindInt64(&stmt, 1, data->guid);
	MsgSQLBindString(&stmt, 2, data->subject, FALSE);
	MsgSQLBindInt(&stmt, 3, data->date);
	MsgSQLBindInt(&stmt, 4, data->sources);
	
	status = MsgSQLExecute(client->storedb, &stmt);
	if (status != 0) {
		retcode = -1;
		goto abort;
	}
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(client->storedb))
		goto abort;

	return 0;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(client->storedb);
	return retcode;
}
