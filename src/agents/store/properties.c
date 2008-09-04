/**
 * \file Code and data to define the location etc. of various properties.
 */

#include <xpl.h>
#include <memmgr.h>
#include "properties.h"

const StorePropValName StorePropTable[] = {
	{ STORE_PROP_ACCESS_CONTROL, "nmap.access-control", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_COLLECTION, "nmap.collection", STORE_PROPTABLE_SO, "collection_guid" },
	{ STORE_PROP_CREATED, "nmap.created", STORE_PROPTABLE_SO, "time_created" },
	{ STORE_PROP_FLAGS, "nmap.flags", STORE_PROPTABLE_SO, "flags" },
	{ STORE_PROP_GUID, "nmap.guid", STORE_PROPTABLE_SO, "guid" },
	{ STORE_PROP_INDEX, "nmap.index", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_LASTMODIFIED, "nmap.lastmodified", STORE_PROPTABLE_SO, "time_modified" },
	{ STORE_PROP_LENGTH, "nmap.length", STORE_PROPTABLE_SO, "size" },
	{ STORE_PROP_TYPE, "nmap.type", STORE_PROPTABLE_SO, "type" },
	{ STORE_PROP_VERSION, "nmap.version", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_NAME, "nmap.name", STORE_PROPTABLE_SO, "filename" },
	{ STORE_PROP_MAIL_IMAPUID, "nmap.mail.imapuid", STORE_PROPTABLE_SO, "imap_uid" },
	{ STORE_PROP_EVENT_ALARM, "nmap.event.alarm", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_EVENT_CALENDARS, "nmap.event.calendars", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_EVENT_END, "nmap.event.end", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_EVENT_LOCATION, "nmap.event.location", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_EVENT_START, "nmap.event.start", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_EVENT_SUMMARY, "nmap.event.summary", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_EVENT_UID, "nmap.event.uid", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_EVENT_STAMP, "nmap.event.stamp", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_MAIL_CONVERSATION, "nmap.mail.conversation", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_MAIL_HEADERLEN, "nmap.mail.headersize", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_MAIL_MESSAGEID, "nmap.mail.messageid", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_MAIL_PARENTMESSAGEID, "nmap.mail.parentmessageid", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_MAIL_SENT, "nmap.mail.sent", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_MAIL_SUBJECT, "nmap.mail.subject", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_CONVERSATION_COUNT, "nmap.conversation.count", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_CONVERSATION_DATE, "nmap.conversation.date", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_CONVERSATION_SUBJECT, "nmap.conversation.subject", STORE_PROPTABLE_NONE, NULL },
	{ STORE_PROP_CONVERSATION_UNREAD, "nmap.conversation.unread", STORE_PROPTABLE_NONE, NULL },
	{ 0, 0, STORE_PROPTABLE_NONE, NULL }
};

/**
 * Given a name of a property, try to fill out the correct StorePropInfo
 * \param	name	Name of the property we want to find (we need to write to this!)
 * \param	prop	Place to fill out the StorePropInfo for that name
 * \return	0
 */
int
StorePropertyByName(char *name, StorePropInfo *prop)
{
	// FIXME - this just does a naive loop. We could speed this up
	// using a hashtable or something if we wanted. Possibly not worth
	// fixing, though...
	const StorePropValName *prop_table = StorePropTable;
	char *p;
	
	// FIXME: we should do a better check of the name before assuming
	// it's an external property. E.g., making sure no-one is using
	// our nmap or bongo namespaces.
	prop->type = STORE_PROP_EXTERNAL;
	prop->name = NULL;
	prop->table = STORE_PROPTABLE_NONE;
	prop->column = NULL;
	
	if (strchr(name, ',') || strchr(name, '/')) {
		prop->type = STORE_PROP_BADPROP;
	}
	p = strchr(name, '.');
	if (!p) {
		prop->type = STORE_PROP_BADPROP;
	}
	if (prop->type == STORE_PROP_BADPROP) {
		return 0;
	}
	
	// FIXME: should convert this name to lower case....
	prop->name = name;
	
	while (prop_table->intval != 0) {
		if (! XplStrCaseCmp(prop_table->name, name)) {
			prop->type = prop_table->intval;
			prop->name = (char *)prop_table->name;
			prop->table = prop_table->db_table;
			prop->column = prop_table->column;
		}
		prop_table++;
	}
	
	return StorePropertyFixup(prop);
}

int
StorePropertyFixup(StorePropInfo *prop)
{
	if (prop->type == STORE_PROP_NONE) {
		const StorePropValName *storeprop = StorePropTable;
		
		// need a name in order to work...
		if (prop->name == NULL) return -1;
		
		do {
			if (strcmp(storeprop->name, prop->name) == 0) {
				prop->type = storeprop->intval;
				prop->table = storeprop->db_table;
				prop->column = storeprop->column;
			}
			storeprop++;
		} while (storeprop->intval != 0);
		
		// anything not in our table is external.
		if (prop->type == STORE_PROP_NONE) 
			prop->type = STORE_PROP_EXTERNAL;
	} else if (prop->name == NULL) {
		const StorePropValName *storeprop = StorePropTable;
		
		do {
			if (storeprop->intval == prop->type) {
				prop->name = (char *)storeprop->name;
				prop->table = storeprop->db_table;
				prop->column = storeprop->column;
			}
			storeprop++;
		} while (storeprop->intval != 0);
	}
	
	switch (prop->table) {
		case STORE_PROPTABLE_SO:
			prop->table_name = "so";
			break;
		default:
			// no other tables used at this point.
			prop->table_name = NULL;
			break;
	}
	
	return 0;
}
