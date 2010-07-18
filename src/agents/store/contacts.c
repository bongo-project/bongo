
#include <config.h>
#include <xpl.h>
#include <bongoutil.h>

#include "messages.h"
#include "stored.h"

static void
SetDocProp(StoreClient *client, StoreObject *contact, char *pname, char *value)
{
	StorePropInfo info;

	if (value != NULL) {
		info.type = STORE_PROP_NONE;
		info.name = pname;
		info.value = value;
		info.table = STORE_PROPTABLE_NONE;
		StorePropertyFixup(&info);
		StoreObjectSetProperty(client, contact, &info);
	}
}


const char * 
StoreProcessIncomingContact(StoreClient *client, StoreObject *contact, char *path)
{
	UNUSED_PARAMETER_REFACTOR(path);
	
	BongoJsonNode *node = NULL;
	char *name = NULL;
	char *email = NULL;
	
	if (GetJson(client, contact, &node, path) != BONGO_JSON_OK)
		goto parse_error;

	if (node->type != BONGO_JSON_OBJECT)
		goto parse_error;

	BongoJsonResult result;
	result = BongoJsonJPathGetString(node, "o:fullName/s", &name);
	if (result != BONGO_JSON_OK) name = NULL;

	// FIXME: want to find the preferred address really.
	result = BongoJsonJPathGetString(node, "o:email/a:0/o:address/s", &email);
	if (result != BONGO_JSON_OK) email = NULL;

	if (name) 	SetDocProp(client, contact, "bongo.contact.name", name);
	if (email)	SetDocProp(client, contact, "bongo.contact.email", email);

	return NULL;

parse_error:
	if (node) BongoJsonNodeFree(node);
	
	return MSG4226BADCONTACT;
}
