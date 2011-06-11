// parse incoming mail messages using GMime

#include <config.h>
#include "stored.h"

#include <unistd.h>
#include <sys/wait.h>

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
	{ "Subject", "nmap.mail.subject" },
	{ NULL, NULL }
};

const char *
StoreProcessIncomingMail(StoreClient *client,
                         StoreObject *document,
                         const char *path)
{
	Connection *spipe = NULL;
	StoreObject conversation;
	StoreConversationData conversation_data;
	struct wanted_header *headers = header_list;
	char *result = NULL;
	char *header_str = NULL;
	
	int commsPipe[2];
	pid_t childpid;
	char readbuffer[4096];
	char *message_subject = NULL;
	
	memset(&conversation_data, 0, sizeof(StoreConversationData));
	memset(&conversation, 0, sizeof(conversation));
	
	// Parse the mail in a sub-process, as this way we can avoid being
	// blown out of the water if we somehow segfault during processing.
	//pipe(commsPipe);
	socketpair(AF_UNIX, SOCK_STREAM, 0, commsPipe);

	if ((childpid = fork()) == -1) {
		goto finish;
	}
	
	if (childpid == 0) {
		// this is the child process; parse the mail here.
		GMimeMessage *message;
		GMimeParser *parser;
		GMimeStream *stream;
		char outstr[1024];
		int fd, outstr_size;
		char prop[XPL_MAX_PATH+1];

		/* close the parent side as we are the child */
		close(commsPipe[0]);

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
			/* close our side of the pipe */
			close(commsPipe[1]);

			// message didn't parse. 
			exit(-1);
		}

		/* create the wrapper for the child side */
		Connection *cpipe = NULL;
		cpipe = ConnAlloc(TRUE);
		if (!cpipe) {
			goto threadcleanup;
		}
		cpipe->socket = commsPipe[1];
		
		while (headers->header != NULL) {
			const char *value = g_mime_object_get_header(GMIME_OBJECT(message), headers->header);
			
			if (value != NULL) {
				outstr_size = snprintf(outstr, sizeof(outstr)-1, "%s\1%s\n", 
					(char *)headers->propname, (char *)value);
				ConnWrite(cpipe, outstr, outstr_size);
			}
			
			headers++;
		}
		
		// treat message ID specially because we want to invent one if it 
		// doesn't already exist.
		if (! message->message_id) {
			snprintf(prop, XPL_MAX_PATH, "%u." GUID_FMT "@%s", document->time_created, document->guid, 
				StoreAgent.agent.officialName);
			prop[XPL_MAX_PATH] = '\0';
			message->message_id = prop;
		}
		outstr_size = snprintf(outstr, sizeof(outstr)-1, "nmap.mail.messageid\1%s\n", 
			message->message_id);
		ConnWrite(cpipe, outstr, outstr_size);
		
		header_str = g_mime_object_get_headers(GMIME_OBJECT(message));
		
		if (header_str != NULL) {
			snprintf(prop, XPL_MAX_PATH, FMT_UINT64_DEC, (uint64_t)strlen(header_str));
			outstr_size = snprintf(outstr, sizeof(outstr)-1, "nmap.mail.headersize\1%s\n", prop);
			ConnWrite(cpipe, outstr, outstr_size);
			g_free(header_str);
		}
		
		threadcleanup:

		// all done, we can quit now
		// FIXME: Why does this crash? : g_object_unref(message);
		if (fd > 0) {
			close(fd);
		}

		/* clean up */
		if (cpipe) {
			ConnClose(cpipe);
			ConnFree(cpipe);
		}

		exit(0);
	}

	waitpid(childpid, NULL, 0);
	
	int nbytes;
	spipe = ConnAlloc(TRUE);
	if (!spipe) {
		goto finish;
	}
	spipe->socket = commsPipe[0];
	
	// from here, we're the parent - need to get the results from the
	// child.
	close(commsPipe[1]);
	
	while ((nbytes = ConnReadLine(spipe, readbuffer, sizeof(readbuffer))) > 0) {
		char *ptr = readbuffer, *pair, *end = readbuffer+nbytes;

		while (ptr <= end) {
			if (*ptr == '\1') {
				*ptr = '\0';
				pair = ++ptr;

				if (*(end-1) == '\n') {
					/* shortcut the rest of the searching */
					*(end-1) = '\0';

					SetDocProp(client, document, readbuffer, pair);

					if (strcmp(readbuffer, "nmap.mail.subject") == 0) {
						message_subject = MemStrdup(pair);
					}
				}
				break;
			}

			ptr++;
		}
	}
	
	// now, see if we can find a matching conversation
	if (message_subject) {
		conversation_data.subject = message_subject;
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
	}
	
finish:
	if (message_subject) MemFree(message_subject);
	if (spipe) {
		ConnClose(spipe);
		ConnFree(spipe);
	}
	
	return result;
}

