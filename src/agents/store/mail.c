// parse incoming mail messages using GMime

#include <config.h>
#include "stored.h"

#include <unistd.h>

#include <glib.h>
#include <gmime/gmime.h>

#include <xpl.h>
#include <memmgr.h>

#include "mail.h"
#include "messages.h"

static void
SetDocProp(StoreClient *client, StoreObject *doc, char *pname, char *value)
{
	StorePropInfo info;
	
	if (value != NULL) {
		info.type = STORE_PROP_NONE;
		info.name = pname;
		info.value = value;
		info.table = STORE_PROPTABLE_NONE;
		StorePropertyFixup(&info);
		StoreObjectSetProperty(client, doc, &info);
	}
}

struct wanted_header {
	const char const *header; 
	const char const *propname;
};

// properties we want to look for and set automatically
static struct wanted_header header_list[] = {
	{ "From", "bongo.from" },
	{ "To", "bongo.to" },
	{ "CC", "bongo.cc" },
	{ "Sender", "bongo.sender" },
	{ "In-Reply-To", "bongo.inreplyto" },
	{ "References", "bongo.references" },
	{ "X-Auth-OK", "bongo.xauthok" },
	{ "List-Id", "bongo.listid" },
	{ "Subject", "bongo.subject" },
	{ NULL, NULL }
};

const char *
StoreProcessIncomingMail(StoreClient *client,
                         StoreObject *document,
                         const char *path)
{
	GMimeMessage *message;
	GMimeParser *parser;
	GMimeStream *stream;
	StoreObject conversation;
	StoreConversationData conversation_data;
	struct wanted_header *headers = header_list;
	char prop[XPL_MAX_PATH+1];
	char *result = NULL;
	char *header_str = NULL;
	int fd;
	
	memset(&conversation_data, 0, sizeof(StoreConversationData));
	memset(&conversation, 0, sizeof(conversation));
	
	// open up the mail
	fd = open(path, O_RDONLY);
	if (fd == -1) return MSG4224CANTREADMBOX;
	
	stream = g_mime_stream_fs_new(fd);
	parser = g_mime_parser_new_with_stream(stream);
	g_mime_parser_set_scan_from (parser, FALSE);
	g_object_unref(stream);
	message = g_mime_parser_construct_message(parser);
	g_object_unref(parser);
	
	if (message == NULL) {
		// message didn't parse. What should we do?
		goto finish;
	}
	
	while (headers->header != NULL) {
		const char *value = g_mime_object_get_header(GMIME_OBJECT(message), headers->header);
		
		if (value != NULL)
			SetDocProp(client, document, (char *)headers->propname, (char *)value);
		
		headers++;
	}
	
	// treat message ID specially because we want to invent one if it 
	// doesn't already exist.
	if (message->message_id) {
		SetDocProp(client, document, "nmap.mail.messageid", message->message_id);
	} else {
		snprintf(prop, XPL_MAX_PATH, "%u." GUID_FMT "@%s", document->time_created, document->guid, 
			StoreAgent.agent.officialName);
		prop[XPL_MAX_PATH] = '\0';
		SetDocProp(client, document, "nmap.mail.messageid", prop);
	}
	
	header_str = g_mime_object_get_headers(GMIME_OBJECT(message));
	
	if (header_str != NULL) {
		snprintf(prop, XPL_MAX_PATH, FMT_UINT64_DEC, (uint64_t)strlen(header_str));
		SetDocProp(client, document, "nmap.mail.headersize", prop);
		g_free(header_str);
	}
	
	// now, see if we can find a matching conversation
	conversation_data.subject = MemStrdup(message->subject);
	conversation_data.date = 0; // FIXME: should be 7 days ago or similar
	
	if (StoreObjectFindConversation(client, &conversation_data, &conversation) != 0) {
		// need to create a conversation for this document
		StoreObject convo_collection;
		
		if (StoreObjectFind(client, STORE_CONVERSATIONS_GUID, &convo_collection)) {
			result = MSG5005DBLIBERR;
			goto finish;
		}
		
		conversation.type = STORE_DOCTYPE_CONVERSATION;
		
		if (StoreObjectCreate(client, &conversation) != 0) {
			// can't create conversation
			result = MSG5005DBLIBERR;
			goto finish;
		}
		
		conversation_data.guid = conversation.guid;
		conversation_data.date = conversation.time_created;
		conversation_data.sources = 0;
		if (StoreObjectSaveConversation(client, &conversation_data)) {
			result = MSG5005DBLIBERR;
			goto finish;
		}
		
		// save setting collection_guid til last, so that the new convo
		// 'appears' atomically
		conversation.collection_guid = STORE_CONVERSATIONS_GUID;
		StoreObjectFixUpFilename(&convo_collection, &conversation);
		if (StoreObjectSave(client, &conversation) != 0) {
			result = MSG5005DBLIBERR;
			goto finish;
		}
	}
	// Link new mail to conversation
	if (StoreObjectLink(client, &conversation, document)) {
		// couldn't put the link in place, erk.
		result = MSG5005DBLIBERR;
		goto finish;
	}
	
finish:
	if (conversation_data.subject != NULL) MemFree(conversation_data.subject);
	g_object_unref(message);
	close(fd);
	
	return result;
}

