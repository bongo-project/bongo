/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005 Novell, Inc. All Rights Reserved.
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

#include <config.h>

#include <xpl.h>
#include <bongoutil.h>
#include <assert.h>
#include <connio.h>

#include "stored.h"
#include "messages.h"

#include "conversations.h"

/* Conversations become invalid after five days */
#define CONVERSATION_AGE (60 * 60 * 24 * 5)

static char *stripPrefixes[] = {
    "re:",
    "SV:",
    "fwd:",
    "r:",
};

static char *
NormalizeSubject(const char *subject)
{
    char *ret;
    char *p;
    
    ret = MemStrdup(subject);
    for (p = ret; *p != '\0'; p++) {
        if (isspace(*p)) {
            char *p1;
            for (p1 = p; *p1 != '\0'; p1++) {
                *p1 = *(p1 + 1);
            }
        }
    }

    return ret;
}

static char *
DisplaySubject(const char *subject)
{
    char *newSubject;
    const char *ptr;
    char *ptr2;
    int length;
    BOOL done;

    newSubject = MemStrdup(subject);
    length = strlen(newSubject);
    
    ptr = subject;

    do {
        done = TRUE;

        if ((*ptr == '[') && (newSubject[length - 1] == ']')) {
            /* Remove enclosing brackets */
            newSubject[--length] = '\0';
            ptr++;
            done = FALSE;
        } else if (*ptr == '[') {
            /* Remove bracketed prefixes (like mailing list names) */
            int depth = 0;
            do {
                if (*ptr == '[') {
                    depth++;
                } else if (*ptr == ']') {
                    depth--;
                }
                ptr++;
            } while (*ptr && (depth > 0));
            done = FALSE;
        } else if (isspace(*ptr)) {
            ptr++;
            done = FALSE;
        } else {
            unsigned int i;
            for (i = 0; i < (sizeof(stripPrefixes) / sizeof(char *)); i++) {
                char *prefix = stripPrefixes[i];
                int length = strlen(prefix);
                if (!XplStrNCaseCmp(ptr, prefix, length)) {
                    ptr += strlen(prefix);
                    done = FALSE;
                }
            }
        }
    } while (!done && *ptr != '\0');

    memmove(newSubject, ptr, strlen(ptr) + 1);
    for (ptr2 = newSubject; *ptr2 != '\0'; ptr2++) {
        if (*ptr2 == '\t') {
            *ptr2 = ' ';
        }
    }

    return newSubject;
}

static int
CreateConversation(DStoreHandle *handle,
                   const char *normalizedSubject,
                   const char *displaySubject,
                   DStoreDocInfo *conversation)
{   
    int ccode;
    // FIXME
    
    // create a new StoreObject for this conversation
    
    // set "bongo.conversation.subject" property?
    
    return 0;
    /*
    memset(conversation, 0, sizeof(DStoreDocInfo));
     
    conversation->guid = 0;
    conversation->type = STORE_DOCTYPE_CONVERSATION;
    conversation->collection = STORE_CONVERSATIONS_GUID;

    conversation->data.conversation.subject = 
        BongoMemStackPushStr(DStoreGetMemStack(handle), normalizedSubject);
    if (!conversation->data.conversation.subject) {
        return -1;
    }

    strncpy(conversation->filename, STORE_CONVERSATIONS_COLLECTION "/",
            sizeof(conversation->filename));

    ccode = DStoreSetDocInfo(handle, conversation);
    if (ccode != 0) {
        return ccode;
    }
    
    return DStoreSetPropertySimple(handle, conversation->guid, 
                                   "bongo.conversation.subject", displaySubject);
	*/
}


/* recompute merge metadata from the given mail into conversation metadata */

#if 0
int
ConversationUpdateWithNewMail(DStoreHandle *handle,
                              DStoreDocInfo *conversation,
                              DStoreDocInfo *mail)
{
    ++conversation->data.conversation.numDocs;

    conversation->data.conversation.sources |= CONVERSATION_SOURCE_ALL;

    /* 'All are...' sources */                
    if (!(mail->flags & STORE_MSG_FLAG_DELETED)) {
        conversation->flags &= ~STORE_MSG_FLAG_DELETED;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_TRASH;
    }
    
    if (!(mail->flags & STORE_MSG_FLAG_JUNK)) {
        conversation->flags &= ~STORE_MSG_FLAG_JUNK;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_JUNK;
    }

    /* 'Any are...' sources */
    if (mail->collection == STORE_MAIL_INBOX_GUID) {
        conversation->data.conversation.sources |= CONVERSATION_SOURCE_INBOX;
    }

    if (mail->flags & STORE_MSG_FLAG_DRAFT) {
        conversation->data.conversation.sources |= CONVERSATION_SOURCE_DRAFTS;
    }
    
    if (mail->flags & STORE_MSG_FLAG_SENT) {
        conversation->data.conversation.sources |= CONVERSATION_SOURCE_SENT;
    }
    
    if (!(mail->flags & STORE_MSG_FLAG_SEEN)) {
        conversation->flags &= ~STORE_MSG_FLAG_SEEN;
        conversation->data.conversation.numUnread++;
    }
    
    if (!conversation->data.conversation.lastMessageDate || 
        (mail->data.mail.sent > conversation->data.conversation.lastMessageDate &&
         !(STORE_MSG_FLAG_DRAFT & mail->flags) && 
         !(STORE_MSG_FLAG_SENT & mail->flags)))
    {
        conversation->data.conversation.lastMessageDate = mail->data.mail.sent;
    }

    /* Clear out mutually exclusive sources */
    if (conversation->data.conversation.sources & CONVERSATION_SOURCE_TRASH) {
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_ALL;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_INBOX;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_STARRED;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_SENT;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_DRAFTS;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_JUNK;
    }
    if (conversation->data.conversation.sources & CONVERSATION_SOURCE_JUNK) {
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_ALL;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_INBOX;
    }

    {
        BongoStringBuilder sb;
        char *p;
        char *pend;
        char *q;
        char *qend;
        int dirty = 0;
        char tmp;

        if (BongoStringBuilderInit(&sb)) {
            return -1;
        }
        
        if (-1 == DStoreGetPropertySBAppend(handle, conversation->guid, "bongo.from",
                                            &sb))
        {
            return -1;
        }

#define NEXTSENDER(_ptr, _endptr)                 \
        _endptr = strchr(_ptr, '\n');             \
        if (!_endptr) {                           \
            BongoStringBuilderDestroy(&sb);        \
            return -1;                            \
        }                                         \
        *_endptr = 0;
        
        for (p = mail->data.mail.from;
             p && *p;
             *pend = '\n', p = pend + 1)
        {
            NEXTSENDER(p, pend);

            for (q = sb.value;
                 q && *q;
                 *qend = '\n', q = qend + 1)
            {
                NEXTSENDER(q, qend);

                if (!strcmp(p, q)) {
                    *qend = '\n';
                    goto skip;
                }
            }

            *pend = '\n';
            tmp = pend[1];
            pend[1] = 0;
            BongoStringBuilderAppend(&sb, p);
            pend[1] = tmp;
            ++dirty;
        skip:
            ;
        }

        if (dirty && 
            DStoreSetPropertySimple(handle, conversation->guid, 
                                    "bongo.from", sb.value)) 
        {
            BongoStringBuilderDestroy(&sb);
            return -1;
        }

        BongoStringBuilderDestroy(&sb);

#undef NEXTSENDER
    }

    return DStoreSetDocInfo(handle, conversation);
}
#endif

// update a conversation when a member has a new flag set.
/** \internal
 * Update a conversation when flags are changed on a member mail
 * \bug Doesn't work for 'all are' flags, only 'any are': STORE_MSG_FLAG_DELETED, ..JUNK
 */
// to fix the 'all are' bugs, we want a counter like .numUnread so that we don't need
// to walk the list of all conversation members again, which is slow.
int
UpdateConversationMemberNewFlag(DStoreHandle *handle,
                                DStoreDocInfo *conversation,
                                uint32_t old_flags,
                                uint32_t new_flags)
{
	if (new_flags & STORE_MSG_FLAG_DRAFT) {
		conversation->data.conversation.sources |= CONVERSATION_SOURCE_DRAFTS;
	}

	if (new_flags & STORE_MSG_FLAG_SENT) {
		conversation->data.conversation.sources |= CONVERSATION_SOURCE_SENT;
	}

	if ((old_flags & STORE_MSG_FLAG_SEEN) && !(new_flags & STORE_MSG_FLAG_SEEN)) {
		// new mail is marked as unread
		if (conversation->data.conversation.numUnread == 0)
			conversation->flags |= STORE_MSG_FLAG_SEEN;
		
		conversation->data.conversation.numUnread++;
	}
	
	if (!(old_flags & STORE_MSG_FLAG_SEEN) && (new_flags & STORE_MSG_FLAG_SEEN)) {
		// new mail is marked as read
		conversation->data.conversation.numUnread--;
		
		if (conversation->data.conversation.numUnread == 0)
			conversation->flags &= ~STORE_MSG_FLAG_SEEN;
	}

	return DStoreSetDocInfo(handle, conversation);
}


/* recompute metadata from scratch for the given conversation */

int 
UpdateConversationMetadata(DStoreHandle *handle,
                           DStoreDocInfo *conversation)
{
    DStoreStmt *stmt;
    DStoreDocInfo info;
    int dcode = 0;
    int numDocs;
    BongoStringBuilder sb;
    int cnt = 0;

    stmt = DStoreFindConversationMembers(handle, conversation->guid);
    if (!stmt) {
        return -1;
    }

    conversation->data.conversation.numUnread = 0;
    conversation->data.conversation.lastMessageDate = 0;
    conversation->data.conversation.numDocs = 0;

    /* Assume it's deleted and read unless a non-deleted or seen message is found */
    conversation->flags |= (STORE_MSG_FLAG_DELETED | STORE_MSG_FLAG_SEEN | STORE_MSG_FLAG_JUNK);

    /* For 'all are...' sources, assume they are unless we find otherwise */
    /* For 'any are...' sources, assume they aren't */
    /* Assume it's in All unless it ends up in junk or trash */
    conversation->data.conversation.sources = CONVERSATION_SOURCE_TRASH | CONVERSATION_SOURCE_JUNK | CONVERSATION_SOURCE_ALL;

    if (conversation->flags & STORE_DOCUMENT_FLAG_STARS) {
        conversation->data.conversation.sources |= CONVERSATION_SOURCE_STARRED;
    } else {
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_STARRED;
    }

    numDocs = 0;
    do {
        dcode = DStoreInfoStmtStep(handle, stmt, &info);
        
        if (dcode == 1 && info.type == STORE_DOCTYPE_MAIL) {
            numDocs++;
            
            /* 'All are...' sources */                
            if (!(info.flags & STORE_MSG_FLAG_DELETED)) {
                conversation->flags &= ~STORE_MSG_FLAG_DELETED;
                conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_TRASH;
            }

            if (!(info.flags & STORE_MSG_FLAG_JUNK)) {
                conversation->flags &= ~STORE_MSG_FLAG_JUNK;
                conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_JUNK;
            }

            /* 'Any are...' sources */
            if (info.collection == STORE_MAIL_INBOX_GUID) {
                conversation->data.conversation.sources |= CONVERSATION_SOURCE_INBOX;
            }

            if (info.flags & STORE_MSG_FLAG_DRAFT) {
                conversation->data.conversation.sources |= CONVERSATION_SOURCE_DRAFTS;
            }

            if (info.flags & STORE_MSG_FLAG_SENT) {
                conversation->data.conversation.sources |= CONVERSATION_SOURCE_SENT;
            }

            if (!(info.flags & STORE_MSG_FLAG_SEEN)) {
                conversation->flags &= ~STORE_MSG_FLAG_SEEN;
                conversation->data.conversation.numUnread++;
            }

            if (!conversation->data.conversation.lastMessageDate || 
                (info.data.mail.sent > conversation->data.conversation.lastMessageDate
                 && !(STORE_MSG_FLAG_DRAFT & info.flags)
                 && !(STORE_MSG_FLAG_SENT & info.flags)))
            {
                conversation->data.conversation.lastMessageDate = info.data.mail.sent;
            }
        }
    } while (dcode == 1);

    /* Clear out mutually exclusive sources */
    if (conversation->data.conversation.sources & CONVERSATION_SOURCE_TRASH) {
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_ALL;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_INBOX;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_STARRED;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_SENT;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_DRAFTS;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_JUNK;
    }
    if (conversation->data.conversation.sources & CONVERSATION_SOURCE_JUNK) {
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_ALL;
        conversation->data.conversation.sources &= ~CONVERSATION_SOURCE_INBOX;
    }

    DStoreStmtEnd(handle, stmt);

    if (dcode != 0) {
        return dcode;
    }
    if (numDocs == 0) {
        return DStoreDeleteDocInfo(handle, conversation->guid);
    } else {
        conversation->data.conversation.numDocs = numDocs;
    }

    if (BongoStringBuilderInit(&sb)) {
        return -1;
    }

    stmt = DStoreFindConversationSenders(handle, conversation->guid);
    if (!stmt) {
        return -1;
    }

    do {
        const char *str;

        dcode = DStoreStrStmtStep(handle, stmt, &str);
        if (1 == dcode) {
            BongoStringBuilderAppend(&sb, str);
        }
        ++cnt;
    } while (1 == dcode);

    DStoreStmtEnd(handle, stmt);

    if (dcode) {
        BongoStringBuilderDestroy(&sb);
        return dcode;
    }

    if (DStoreSetPropertySimple(handle, conversation->guid, "bongo.from", sb.value)) {
        BongoStringBuilderDestroy(&sb);
        return -1;
    }

    BongoStringBuilderDestroy(&sb);
         
    return DStoreSetDocInfo(handle, conversation);
}


int 
GetConversation(DStoreHandle *handle,
                DStoreDocInfo *info,
                DStoreDocInfo *conversationOut)
{
    char *displaySubject;
    char *normalizedSubject;
    int32_t modifiedAfter;
    int dcode;

    displaySubject = DisplaySubject(info->data.mail.subject ? info->data.mail.subject : "(no subject)");
    normalizedSubject = NormalizeSubject(displaySubject);

    if (info->data.mail.sent > CONVERSATION_AGE) {
        modifiedAfter = info->data.mail.sent - CONVERSATION_AGE;
    } else {
        modifiedAfter = 0;
    }
/*
    dcode = DStoreGetConversation(handle, 
                                  normalizedSubject, 
                                  modifiedAfter, 
                                  conversationOut);
*
    
    if (dcode < 0) {
        goto done;
    }
    if (dcode == 0) {
        /* Didn't find a conversation, create a new one */
        dcode = CreateConversation(handle, 
                                   normalizedSubject, 
                                   displaySubject,
                                   conversationOut);
        if (dcode < 0) {
            goto done;
        }
    } else {
        dcode = 0;
    }
    
    info->conversation = conversationOut->guid;
    
done :
    MemFree(normalizedSubject);
    MemFree(displaySubject);
    return dcode;
}

void
GetConversationSourceMask(const char *sourceName,
                          uint32_t *required)
{
    if (!sourceName) {
        *required = 0;
        return;
    }

    if (!XplStrCaseCmp(sourceName, "all")) {
        *required = CONVERSATION_SOURCE_ALL;
    } else if (!XplStrCaseCmp(sourceName, "inbox")) {
        *required = CONVERSATION_SOURCE_INBOX;
    } else if (!XplStrCaseCmp(sourceName, "starred")) {
        *required = CONVERSATION_SOURCE_STARRED;
    } else if (!XplStrCaseCmp(sourceName, "sent")) {
        *required = CONVERSATION_SOURCE_SENT;
    } else if (!XplStrCaseCmp(sourceName, "drafts")) {
        *required = CONVERSATION_SOURCE_DRAFTS;
    } else if (!XplStrCaseCmp(sourceName, "junk")) {
        *required = CONVERSATION_SOURCE_JUNK;
    } else if (!XplStrCaseCmp(sourceName, "trash")) {
        *required = CONVERSATION_SOURCE_TRASH;
    } else {
        *required = 0;
    }
}
