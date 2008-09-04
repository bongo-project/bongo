/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005-6 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

/* Routines for parsing and manipulating mail messages */

#include <config.h>
#include "stored.h"

#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>
#include <bongoutil.h>

#include "mail.h"
// #include "conversations.h"
#include "mail-parser.h"
#include "messages.h"

typedef struct {
    StoreObject *document;

    BongoStringBuilder from;
    BongoStringBuilder sender;
    BongoStringBuilder to;
    BongoStringBuilder cc;
    BongoStringBuilder bcc;
    BongoStringBuilder xAuthOK;
    BongoStringBuilder references;

    char *subject;
    char *messageId;
    char *parentMessageId;
    char *inReplyTo;    
    char *listId;

    BOOL haveListId;

    uint64_t headerStartOffset;
    uint64_t headerSize;
} IncomingParseData;

static void 
IncomingParticipantCb(void *datap, MailHeaderName name, MailAddress *address)
{
    IncomingParseData *data = datap;
    const char *type = NULL;
    char buf[1024];
    snprintf(buf, 1024, "%s%s<%s>\n", 
             address->displayName ? address->displayName : "",
             address->displayName ? " " : "",
             address->address);

    switch(name) {        
    case MAIL_FROM:
        BongoStringBuilderAppend(&data->from, buf);
        type = "from";
        break;
    case MAIL_SENDER:
        BongoStringBuilderAppend(&data->sender, buf);
        type = "sender";

        /* For listservs that don't set a good listid, we use the
         * sender.  Save the sender as the listid if no listid
         * has been saved. */
        if (!data->listId) {
            snprintf(buf, 1024, "<%s>", address->address);
            data->listId = MemStrdup(buf);
        }
        
        break;
    case MAIL_TO:
        BongoStringBuilderAppend(&data->to, buf);
        type = "to";
        break;
    case MAIL_CC:
        BongoStringBuilderAppend(&data->cc, buf);
        type = "cc";
        break;
    case MAIL_BCC:
        BongoStringBuilderAppend(&data->bcc, buf);
        type = "bcc";
        break;
    case MAIL_X_AUTH_OK:
        BongoStringBuilderAppend(&data->xAuthOK, buf);
        type = "xAuthOK";
        break;
    case MAIL_X_BEEN_THERE:
    case MAIL_X_LOOP : 
        snprintf(buf, 1024, "<%s>", address->address);

        data->haveListId = TRUE;
        
        if (data->listId) {
            MemFree(data->listId);
        }
        data->listId = MemStrdup(buf);
        break;
    default :
        /* Ignore */
        return;
    }

    Log(LOG_DEBUG, "IPCB got %s %s <%s>\n", type, address->displayName, address->address);
}

static void
IncomingUnstructuredCb(void *datap, MailHeaderName name, const char *headerName, const char *buffer)
{
    IncomingParseData *data = datap;

    switch(name) {
    case MAIL_SUBJECT:
        data->subject = MemStrdup(buffer);
        break;
    case MAIL_SPAM_FLAG :
        if (tolower(buffer[0]) == 'y') {
            data->document->type |= STORE_MSG_FLAG_JUNK;
        }
        break;
    case MAIL_LIST_ID:
        /* If no proper listid is given, the sender will be saved
         * in the listid */
        data->haveListId = TRUE;
        break;
    default:
        if (!XplStrCaseCmp(headerName, "X-Listprocessor-Version")) {
            /* If no proper listid is given, the sender will be saved
             * in the listid */
            data->haveListId = TRUE;
        }
        break;
    }
}

static void
IncomingDateCb(void *datap, MailHeaderName name, uint64_t date)
{
    // IncomingParseData *data = datap;

    switch(name) {
    case MAIL_DATE:
        // FIXME
        // data->info->data.mail.sent = date;
        break;
    default :
        /* Ignore */
        return;
    }
}

static void
IncomingOffsetCb(void *datap, MailHeaderName name, uint64_t offset)
{
    IncomingParseData *data = datap;

    switch(name) {
    case MAIL_OFFSET_BODY_START:
        data->headerSize = offset - data->headerStartOffset;
        break;
    default :
        /* Ignore */
        return;
    }
}

static void
IncomingMessageIdCb(void *datap, MailHeaderName name, const char *messageId)
{
    IncomingParseData *data = datap;
    
    switch(name) {
    case MAIL_MESSAGE_ID:
        if (!data->messageId) {
            data->messageId = MemStrdup(messageId);
        }
        break;
    case MAIL_IN_REPLY_TO :
        if (!data->inReplyTo) {
            data->inReplyTo = MemStrdup(messageId);

            MemFree(data->parentMessageId);
            data->parentMessageId = MemStrdup(messageId);
        }

        break;
    case MAIL_REFERENCES :
        if (data->references.len > 0) {
            BongoStringBuilderAppend(&data->references, ", ");
        }

        BongoStringBuilderAppend(&data->references, messageId);

        if (!data->inReplyTo) {
            /* If In-Reply-To isn't set, use the last References:
             * messageId as the parent */
            MemFree(data->parentMessageId);
            data->parentMessageId = MemStrdup(messageId);
        }
            
        break;
    default :
        /* Ignore */
        return;
    }
}


/* returns: NULL on success
            errormsg o/w
*/

static void
SetDocProp(StoreClient *client, StoreObject *doc, char *pname, char *value)
{
	StorePropInfo info;
	
	if (value != NULL) {
		info.type = STORE_PROP_NONE;
		info.name = pname;
		info.value = value;
		StorePropertyFixup(&info);
		StoreObjectSetProperty(client, doc, &info);
	}
}

const char *
StoreProcessIncomingMail(StoreClient *client,
                         StoreObject *document,
                         const char *path)
{
	const char *result = NULL;
	MailParser *p;
	IncomingParseData data = {0, };
	char prop[XPL_MAX_PATH + 1];
	StoreObject conversation;
	StoreConversationData conversation_data;
	
	FILE *fh;

	fh = fopen(path, "rb");
	if (!fh) return MSG4224CANTREADMBOX;

	data.headerStartOffset = 0;
	data.document = document;

	p = MailParserInit(fh, &data);
	MailParserSetParticipantsCallback(p, IncomingParticipantCb);
	MailParserSetUnstructuredCallback(p, IncomingUnstructuredCb);
	MailParserSetDateCallback(p, IncomingDateCb);
	MailParserSetOffsetCallback(p, IncomingOffsetCb);
	MailParserSetMessageIdCallback(p, IncomingMessageIdCb);

	if (MailParseHeaders(p) == -1) {
		result = MSG4226BADEMAIL;
		goto finish;
	}
	
	if (data.from.value) {
		RFC1432_EncodedWord ew;
		
		if (EncodedWordInit(&ew, data.from.value) == 0) {
			SetDocProp(client, document, "bongo.from", ew.decoded);
			EncodedWordFinish(&ew);
		} else {
			SetDocProp(client, document, "bongo.from", data.from.value);
		}
	}
	
	SetDocProp(client, document, "bongo.to", data.to.value);
	SetDocProp(client, document, "bongo.sender", data.sender.value);
	SetDocProp(client, document, "bongo.cc", data.cc.value);
	SetDocProp(client, document, "bongo.inreplyto", data.inReplyTo);
	SetDocProp(client, document, "bongo.references", data.references.value);
	SetDocProp(client, document, "bongo.xauthok", data.xAuthOK.value);
	SetDocProp(client, document, "bongo.listid", data.listId);

	if (data.messageId) {
		SetDocProp(client, document, "nmap.mail.messageid", data.messageId);
	} else {
		snprintf(prop, XPL_MAX_PATH, "%u." GUID_FMT "@%s", document->time_created, document->guid, 
			StoreAgent.agent.officialName);
		SetDocProp(client, document, "nmap.mail.messageid", prop);
	}
	
	SetDocProp(client, document, "nmap.mail.parentmessageid", data.parentMessageId);
	SetDocProp(client, document, "nmap.mail.subject", data.subject);
	
	if (data.headerSize > 0) {
		snprintf(prop, XPL_MAX_PATH, FMT_UINT64_DEC, data.headerSize);
		SetDocProp(client, document, "nmap.mail.headersize", prop);
	}
	
	memset(&conversation_data, 0, sizeof(StoreConversationData));
	memset(&conversation, 0, sizeof(conversation));
	conversation_data.subject = MemStrdup(data.subject);
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
	
	// fix up conversation meta-data?
	// TODO
	
finish:
	if (conversation_data.subject) MemFree(conversation_data.subject);
	MemFree(data.from.value);
	MemFree(data.to.value);
	MemFree(data.cc.value);
	MemFree(data.sender.value);
	MemFree(data.messageId);
	MemFree(data.inReplyTo);
	MemFree(data.references.value);
	MemFree(data.parentMessageId);
	MemFree(data.xAuthOK.value);
	MemFree(data.listId);

	MailParserDestroy(p);

	fclose(fh);

	return result;
}

