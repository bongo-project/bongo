/* Object model for Bongo store. This is used to access the SQLite database
 * for a given store indirectly.
 * 
 * Refer to diagrams/store-sqlite-schema.dot for diagram of schema, which 
 * this attempts to represent.
 */

#include "stored.h"

#ifndef OBJECT_MODEL_H
#define OBJECT_MODEL_H

#include "query-builder.h"

XPL_BEGIN_C_LINKAGE

typedef struct {
	uint64_t	guid;
	char *		subject;
	uint32_t	date;
	uint32_t	sources;
} StoreConversationData;

typedef struct _StoreObjectPropertyIterator StoreObjectPropertyIterator;
typedef void (*bongoPropertyIteratorCallback)(StoreClient *client, 
	char *name, char *value, StoreObjectPropertyIterator *iterator);

struct _StoreObjectPropertyIterator {
	int 		resultcode;
	BOOL		all;
	
	StorePropInfo  *prop_list;
	int             prop_count;
	
	bongoPropertyIteratorCallback callback;
};

typedef struct _StoreObjectIterator StoreObjectIterator;
typedef void (*bongoIteratorCallback)(StoreClient *client, StoreObject *object, StoreObjectIterator *iterator);

struct _StoreObjectIterator {
	int 		resultcode;
	uint32_t	flags;
	
	StorePropInfo  *prop_list;
	int             prop_count;
	
	bongoIteratorCallback callback;
	bongoPropertyIteratorCallback prop_callback;
	
};

/* Functions for operating on these objects */

int StoreObjectFind(StoreClient *client, uint64_t guid, StoreObject *object);
int StoreObjectFindNext(StoreClient *client, uint64_t guid, StoreObject *object);
int StoreObjectFindByFilename(StoreClient *client, const char *filename, StoreObject *object);
int StoreObjectFindConversation(StoreClient *client, StoreConversationData *data, StoreObject *conversation);

int StoreObjectCreate(StoreClient *client, StoreObject *object);
int StoreObjectSave(StoreClient *client, StoreObject *object);
int StoreObjectSaveConversation(StoreClient *client, StoreConversationData *data);
int StoreObjectRemove(StoreClient *client, StoreObject *object);
int StoreObjectRepair(StoreClient *client, StoreObject *object);

int StoreObjectCheckAuthorization(StoreClient *client, StoreObject *object, int prop);
int StoreObjectLink(StoreClient *client, StoreObject *document, StoreObject *related);
int StoreObjectLinkByGuids(StoreClient *client, uint64_t document, uint64_t related);

int StoreObjectUnlink(StoreClient *client, StoreObject *document, StoreObject *related);
int StoreObjectUnlinkAll(StoreClient *client, StoreObject *object);
int StoreObjectUnlinkFromConversation(StoreClient *client, StoreObject *mail);
int StoreObjectUnlinkByGuids(StoreClient *client, uint64_t document, uint64_t related);

int StoreObjectAddACL(StoreClient *client, StoreObject *object, StorePrincipalType principal, StorePrivilege priv,
	BOOL deny, const char *who);
int StoreObjectRemoveACL(StoreClient *client, StoreObject *object, StorePrincipalType principal, StorePrivilege priv, const char *who);
int StoreObjectListACL(StoreClient *client, StoreObject *object);

int StoreObjectSetProperty(StoreClient *client, StoreObject *object, StorePropInfo *prop);
int StoreObjectSetExternalProperty(StoreClient *client, StoreObject *object, StorePropInfo *prop);
int StoreObjectSetFilename(StoreObject *object, const StoreObject *collection, const char *filename);
void StoreObjectFixUpFilename(StoreObject *container, StoreObject *object);
void StoreObjectUpdateModifiedTime(StoreObject *object);

// iterators on store contents.
int StoreObjectIterQueryBuilder(StoreClient *client, QueryBuilder *builder, BOOL show_total);
void StoreObjectIterQueryBuilderPropResult(StoreClient *client, 
	int properties, MsgSQLStatement *stmt, QueryBuilder *builder);

int StoreObjectIterCollectionContents(StoreClient *client, StoreObject *collection, 
	int start, int end, uint32_t flagsmask, uint32_t flags,
	StorePropInfo *props, int propcount,
	const char *safe_query, const char *unsafe_query,
	BOOL show_total);
int StoreObjectIterSubcollections(StoreClient *client, StoreObject *container);
int StoreObjectIterLinks(StoreClient *client, StoreObject *document, BOOL reverse);
int StoreObjectIterConversationMails(StoreClient *client, StoreObject *conversation,
	StorePropInfo *props, int propcount);
int StoreObjectIterDocinfo(StoreClient *client, StoreObject *document, 
	StorePropInfo *props, int propcount, BOOL mainprops);
int StoreObjectIterProperties(StoreClient *client, StoreObject *document);

// misc operators
void StoreOutputProperty(StoreClient *client, StorePropertyType type, const char *name, const char *value);

// "dangerous" functions which hit the DB directly
int StoreObjectCopyInfo(StoreClient *client, StoreObject *old, StoreObject *newobj);
int StoreObjectUpdateImapUID(StoreClient *client, StoreObject *object);
int StoreObjectRenameSubobjects(StoreClient *client, StoreObject *container, const char *new_root);
int StoreObjectRemoveCollection(StoreClient *client, StoreObject *collection);

void StorePropFixupNames(StorePropInfo *props, int propcount);
int  StoreObjectDBCreate(StoreClient *client);
int  StoreObjectDBCheckSchema(StoreClient *client, BOOL new_install);

XPL_END_C_LINKAGE

#endif
