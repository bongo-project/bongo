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

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>
#include <bongoutil.h>

#include "mail.h"
#include "conversations.h"
#include "mail-parser.h"
#include "messages.h"

typedef struct {
    DStoreHandle *handle;
    DStoreDocInfo *info;

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

#if 0
    printf("got %s %s <%s>\n", type, address->displayName, address->address);
#endif
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
            data->info->flags |= STORE_MSG_FLAG_JUNK;
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
    IncomingParseData *data = datap;

    switch(name) {
    case MAIL_DATE:
        data->info->data.mail.sent = date;
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
        data->info->data.mail.headerSize = offset - data->headerStartOffset;
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

const char *
StoreProcessIncomingMail(StoreClient *client,
                         DStoreDocInfo *info,
                         const char *path,
                         DStoreDocInfo *conversationOut,
                         uint64_t forceConversationGuid)
{
    const char *result = NULL;
    MailParser *p;
    IncomingParseData data = {0, };
    DStoreDocInfo oldConversation;
    BOOL haveOldConversation;
    BOOL isNewConversation;
    
    FILE *fh;
    
    fh = fopen(path, "rb");
    if (!fh) {
        return MSG4224CANTREADMBOX;
    }
    
    fseek(fh, info->start + info->headerlen, SEEK_SET);    

    data.headerStartOffset = info->start + info->headerlen;
    data.handle = client->handle;
    data.info = info;

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



    /* FIXME: temp */
    if (DStoreSetPropertySimple(client->handle, info->guid, 
                                "bongo.from", data.from.value ? data.from.value : "") ||
        DStoreSetPropertySimple(client->handle, info->guid, 
                                "bongo.to", data.to.value ? data.to.value : "") ||
        DStoreSetPropertySimple(client->handle, info->guid, 
                                "bongo.sender", data.sender.value ? data.sender.value : "") ||
        DStoreSetPropertySimple(client->handle, info->guid, 
                                "bongo.cc", data.cc.value ? data.cc.value : "") ||
        DStoreSetPropertySimple(client->handle, info->guid, 
                                "bongo.inreplyto", data.inReplyTo ? data.inReplyTo: "") ||
        DStoreSetPropertySimple(client->handle, info->guid, 
                                "bongo.references", data.references.value ? data.references.value : "") ||
        DStoreSetPropertySimple(client->handle, info->guid, 
                                "bongo.xauthok", data.xAuthOK.value ? data.xAuthOK.value : "") ||
        DStoreSetPropertySimple(client->handle, info->guid,
                                "bongo.listid", data.haveListId && data.listId ? data.listId : ""))
    {
        result = MSG5005DBLIBERR;
        goto finish;
    }

    if (data.messageId) {
        info->data.mail.messageId = 
            BongoMemStackPushStr(&client->memstack, data.messageId);
    } else {
        info->data.mail.messageId =
            BONGO_MEMSTACK_ALLOC(&client->memstack, MAXEMAILNAMESIZE + 1);
        snprintf(info->data.mail.messageId, MAXEMAILNAMESIZE + 1, "%u.%llu@%s",
            info->timeCreatedUTC, info->guid, StoreAgent.agent.officialName);
    }
    if (data.parentMessageId) {
        info->data.mail.parentMessageId = 
            BongoMemStackPushStr(&client->memstack, data.parentMessageId);
    }
    if (data.from.value) {
        info->data.mail.from = 
            BongoMemStackPushStr(&client->memstack, data.from.value);
    }
    if (data.subject) {
        info->data.mail.subject = 
            BongoMemStackPushStr(&client->memstack, data.subject);
    }
    if (data.haveListId && data.listId) {
        info->data.mail.listId = BongoMemStackPushStr(&client->memstack, data.listId);
    }

    if (info->conversation != 0 && 
        DStoreGetDocInfoGuid(client->handle, info->conversation, &oldConversation) == 1) {
        haveOldConversation = TRUE;
    } else {
        haveOldConversation = FALSE;
    }

    if (forceConversationGuid != 0) {
        if (DStoreGetDocInfoGuid(client->handle, forceConversationGuid, conversationOut) != 1) {
            result = MSG4202CONVERSATIONNOTFOUND;
            goto finish;
        }
    } else {
        if (GetConversation(client->handle, info, conversationOut)) {
            /* FIXME: need better error reporting for this stuff */
            result = MSG5005DBLIBERR; 
            goto finish;
        }
    }

    /* Re-store the mail with the data and conversationid set */
    info->conversation = conversationOut->guid;
    if (DStoreSetDocInfo(client->handle, info)) {
        result = MSG5005DBLIBERR;
        goto finish;
    }

    
    if (haveOldConversation) {
        UpdateConversationMetadata(client->handle, &oldConversation);
    } 

    if (!haveOldConversation || oldConversation.guid != conversationOut->guid) {
        if (ConversationUpdateWithNewMail(client->handle, conversationOut, info)) {
            result = MSG5005DBLIBERR;
            goto finish;
        }
    }

finish:

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

