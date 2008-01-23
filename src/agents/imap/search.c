/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
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
#include "imapd.h"
#include <msgdate.h>
#include "search.h"
#include <bongostream.h>

long SearchHandleKey(ImapSession *session, char **keyString, SearchKey *key);
static long SearchHandleSubKey(ImapSession *session, char **keyString, SearchKey *key);
typedef long (* SearchRemainingMatch)(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *result);
long ReadSourceFile(void *source, char *buffer, unsigned long maxRead);
long ReadSourceConn(void *source, char *buffer, unsigned long maxRead);

BongoKeywordIndex *SearchKeyIndex = NULL;

long
ReadSourceFile(void *source, char *buffer, unsigned long maxRead)
{
    return(fread(buffer, 1, maxRead, (FILE *)source));
}

long
ReadSourceConn(void *source, char *buffer, unsigned long maxRead)
{
    return(ConnRead((Connection *)source, buffer, maxRead));
}

__inline static void
MarkedMatchesPreserve(SearchKey *key)
{
    long readNum = 0;
    long writeNum = 0;

    for (;;) {
        if (key->matchList[readNum] != -1) {
            if (key->matchList[readNum] < -1) {
                key->matchList[writeNum] = -2 - key->matchList[readNum];
                writeNum++;
            }
            readNum++;
            continue;
        }
        break;
    }

    if (writeNum < readNum) {
        key->matchList[writeNum] = -1;
        key->matchDropped = TRUE;
    }
}

__inline static void
MarkedMatchesRemove(SearchKey *key)
{
    unsigned long readNum = 0;
    unsigned long writeNum = 0;

    for (;;) {
        if (key->matchList[readNum] != -1) {
            if (key->matchList[readNum] > -1) {
                key->matchList[writeNum] = key->matchList[readNum];
                writeNum++;
            }
            readNum++;
            continue;
        }
        break;
    }
    if (writeNum < readNum) {
        key->matchList[writeNum] = -1;
        key->matchDropped = TRUE;
    }
}


__inline static void
MarkMatch(SearchKey *key, unsigned long index)
{
    /* make sure that it is not already marked */
    if (key->matchList[index] > -1) {
        key->matchList[index] = -2 - key->matchList[index];
    }
}

__inline static void
MarkMatches(SearchKey *key, SearchKey *newKey)
{
    unsigned long readNum = 0;
    unsigned long writeNum = 0;

    for (;;) {
        if (newKey->matchList[readNum] != -1) {
            for (;;) {
                if (key->matchList[writeNum] != newKey->matchList[readNum]) {
                    writeNum++;
                    continue;
                }
                MarkMatch(key, writeNum);
                writeNum++;
                break;
            }
            readNum++;
            continue;
        }
        break;
    }
}


__inline static long
SearchKeyObjectDuplicate(SearchKey *key, SearchKey *newKey)
{
    memset(newKey, 0, sizeof(SearchKey));

    strcpy(newKey->charset, key->charset);
    newKey->matchDropped = key->matchDropped;
    newKey->messageCount = key->messageCount;
    newKey->matchList = MemMalloc((key->messageCount + 1) * sizeof(long));
    if (newKey->matchList) {
        long num = 0;

        for (;;) {
            if (key->matchList[num] != -1) {
                newKey->matchList[num] = key->matchList[num];
                num++;
                continue;
            }
            break;
        }

        newKey->matchList[num] = -1;

        return(STATUS_CONTINUE);
    }

    return(STATUS_MEMORY_ERROR);
}

__inline static long
SearchKeyObjectCreate(SearchKey *key, char *charset, unsigned long msgCount)
{
    unsigned long i;
    unsigned long len;

    memset(key, 0, sizeof(SearchKey));

    len = strlen(charset);
    if (len >= sizeof(key->charset)) {
        len = sizeof(key->charset) - 1;
    }

    memcpy(key->charset, charset, len);
    key->charset[len] = '\0';

    key->matchList = MemMalloc((msgCount + 1) * sizeof(long));
    if (!key->matchList) {
        MemFree(key->charset);
        return(STATUS_MEMORY_ERROR);
    }

    for (i = 0; i < msgCount; i++) {
        key->matchList[i] = i;
    }
    key->matchList[msgCount] = -1;

    key->messageCount = msgCount;
    
    return(STATUS_CONTINUE);
}

__inline static void
SearchKeyObjectFree(SearchKey *key)
{
    MemFree(key->matchList);

    if (key->stream) {
        BongoStreamFree(key->stream);
    }
}

__inline static long
SearchStringToUtf8(SearchKey *key, char *in, char **out)
{
    char buffer[SEARCH_MAX_UTF8_STRING];
    long len;

    if (!key->stream) {
        key->stream = BongoStreamCreate(NULL, 0, FALSE);
        if (!key->stream) {
            return(STATUS_MEMORY_ERROR);
        }

        if(BongoStreamAddCodec(key->stream, key->charset, FALSE)) {
            return(STATUS_CHARSET_NOT_SUPPORTED);
        }
    }

    key->stream->first->EOS = TRUE;
    BongoStreamPut(key->stream, in, strlen(in));
    len = BongoStreamGet(key->stream, buffer, sizeof(buffer));
    buffer[len] = '\0';
    *out = MemStrdup(buffer);
    return(STATUS_CONTINUE);
}

__inline static long
GetSearchStringArg(ImapSession *session, SearchKey *key, char **keyString, char **utf8SearchString)
{
    long ccode;
    char *searchString;

    if((ccode = GrabArgumentEx(session, (unsigned char **)keyString, (unsigned char **)&(searchString), TRUE)) == STATUS_CONTINUE) {
        if (XplStrCaseCmp(key->charset, "utf-8") == 0) {
            *utf8SearchString = searchString;
            return(STATUS_CONTINUE);
        } 
        
        if ((ccode = SearchStringToUtf8(key, searchString, utf8SearchString)) == STATUS_CONTINUE) {
            MemFree(searchString);
            return(STATUS_CONTINUE);
        }
        MemFree(searchString);
    }
    return(ccode);
}

__inline static long
ImapDateArgToUtc(char *dateText, unsigned long *dateUtc)
{
    char *dayPtr;
    char *monthPtr;
    char *yearPtr;
    unsigned long month;

    dayPtr = dateText;
    monthPtr = strchr(dateText, '-');
    if (monthPtr) {
        monthPtr++;
        yearPtr = strchr(monthPtr, '-');
        if (yearPtr) {
            *yearPtr = '\0';
            month = MsgLookupRFC822Month(monthPtr);
            *yearPtr = ' ';
            yearPtr++;
            *dateUtc = MsgGetUTC((unsigned long)atol(dayPtr), (unsigned long)month, (unsigned long)atol(yearPtr), 0, 0, 0);
            return(STATUS_CONTINUE);
        }
    }

    return(STATUS_INVALID_ARGUMENT);
}

__inline static long
GetDateArg(ImapSession *session, char **keyString, time_t *date)
{
    long ccode;
    char *dateText;
    
    if((ccode = GrabArgumentEx(session, (unsigned char **)keyString, (unsigned char **)&(dateText), TRUE)) == STATUS_CONTINUE) {
        ccode = ImapDateArgToUtc(dateText, date);
        MemFree(dateText);
        return(ccode);
    }
    return(ccode);
}

__inline static long
GetMessageSentDate(ImapSession *session, uint64_t guid, unsigned long headerLen, time_t *dateUtc)
{
    long ccode;
    size_t len;
    char *dateString;

    if ((ccode = NMAPSendCommandF(session->store.conn, "READ %llx 0 %lu\r\n", guid, headerLen)) != -1) {
        if ((ccode = NMAPReadPropertyValueLength(session->store.conn, "nmap.document", &len)) == 2001) {
            if ((dateString = BongoStreamGrabRfc822Header(ReadSourceConn, session->store.conn, (unsigned long)len, "Date")) != NULL) {
                if (NMAPReadCrLf(session->store.conn) == 2) {
                    *dateUtc = MsgParseRFC822Date(dateString);
                    MemFree(dateString);
                    return(STATUS_CONTINUE);
                }
                return(STATUS_NMAP_PROTOCOL_ERROR);
            }
            return(STATUS_MEMORY_ERROR);
        }
        return(CheckForNMAPCommError(ccode));
    }
    return(STATUS_NMAP_COMM_ERROR);
}

__inline static long
GetNumericArg(ImapSession *session, char **keyString, unsigned long *num)
{
    long ccode;
    unsigned char *numText;
    long longNum;

    if((ccode = GrabArgumentEx(session, (unsigned char **)keyString, &numText, TRUE)) == STATUS_CONTINUE) {
        longNum = atol(numText);
        MemFree(numText);
        if (longNum >= 0) {
            *num = (unsigned long)longNum;
            return(STATUS_CONTINUE);
        }
        return(STATUS_INVALID_ARGUMENT);
    }
    return(ccode);
}

__inline static long
GetMessageSetArg(ImapSession *session, char **keyString, long **messageSet)
{
    long ccode;
    unsigned char *setText;
    char *nextRange;
    unsigned long start;
    unsigned long end;
    MessageInformation *messageList = &(session->folder.selected.message[0]);
    unsigned long messageCount = session->folder.selected.messageCount;
    long *workSet;

    if((ccode = GrabArgumentEx(session, (unsigned char **)keyString, &setText, TRUE)) == STATUS_CONTINUE) {
        nextRange = setText;
        if ((workSet = MemMalloc0(sizeof(long) * session->folder.selected.messageCount)) != NULL) {
            do {
                if ((ccode = GetMessageRange(messageList, messageCount, &nextRange, &start, &end, TRUE)) == STATUS_CONTINUE) {
                    end++;
                    do {
                        workSet[start] = 1;
                        start++;
                    } while(start < end);
                    continue;
                } else if (ccode == STATUS_UID_NOT_FOUND) {
                    continue;
                }
                MemFree(setText);
                MemFree(workSet);
                return(ccode);
            } while (nextRange);

            MemFree(setText);
            *messageSet = workSet;
            return(STATUS_CONTINUE);
        }

        ccode = STATUS_MEMORY_ERROR;
        MemFree(setText);
    }
    return(ccode);
}


__inline static long
FullListNoMatch(SearchKey *key)
{
    key->matchList[0] = -1;
    return(STATUS_CONTINUE);
}

__inline static long
FullListProcessMatch(ImapSession *session, SearchKey *key, char *response)
{
    long ccode;
    char *ptr;
    unsigned long sequenceNum;

    ptr = strchr(response, ' ');
    if (ptr) {
        ptr++;
        ccode = UidToSequenceNum(&session->folder.selected.message[0], session->folder.selected.messageCount, (uint32_t)HexToUInt64(ptr, NULL), &sequenceNum);
        if (ccode == STATUS_CONTINUE) {
            key->matchList[sequenceNum] = -2 - key->matchList[sequenceNum];
            return(STATUS_CONTINUE);
        }
        return(ccode);
    }
    return(STATUS_NMAP_PROTOCOL_ERROR);
}

__inline static long
FullListSubstringSearch(ImapSession *session, SearchKey *key, char *searchString, char *subcommand)
{
    long ccode;

    if (NMAPSendCommandF(session->store.conn, "SEARCH %llx %s \"%s\"\r\n", session->folder.selected.info->guid, subcommand, searchString) != -1) {
        for (;;) {
            ccode = NMAPReadResponse(session->store.conn, session->store.response, sizeof(session->store.response), TRUE);
            if (ccode == 2001) {
                FullListProcessMatch(session, key, session->store.response + 5);
                continue;
            }
            break;
        }
        if (ccode == 1000) {
            MarkedMatchesPreserve(key);
            return(STATUS_CONTINUE);
        }
        
        return(CheckForNMAPCommError(ccode));
    }
    return(STATUS_NMAP_COMM_ERROR);
}


/* SearchRemainingMatch start */
static long
SearchRemainingMatchFlag(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    unsigned long flag = (unsigned long)arg2;

    *found = ((message->flags & flag) != 0);
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchFlagNot(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    unsigned long flag = (unsigned long)arg2;

    *found = ((message->flags & flag) == 0);
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchNew(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    *found = (((message->flags & STORE_MSG_FLAG_RECENT) != 0) && ((message->flags & STORE_MSG_FLAG_SEEN) == 0));
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchBefore(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    unsigned long date = *(unsigned long *)arg2;

    *found = (message->internalDate < date);
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchOn(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    unsigned long date = *(unsigned long *)arg2;

    *found = ((message->internalDate >= date) && (message->internalDate < date + (60 * 60 * 24)));
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchSince(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    unsigned long date = *(unsigned long *)arg2;

    *found = (message->internalDate >= date);
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchSentBefore(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    long ccode;
    time_t date = *(unsigned long *)arg2;
    time_t messageDateUtc = 0;

    if ((ccode = GetMessageSentDate(session, message->guid, message->headerSize, &messageDateUtc)) == STATUS_CONTINUE) {
        *found = (messageDateUtc < date);
        return(STATUS_CONTINUE);
    }
    return(ccode);
}

static long
SearchRemainingMatchSentOn(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    long ccode;
    time_t date = *(unsigned long *)arg2;
    time_t messageDateUtc = 0;

    if ((ccode = GetMessageSentDate(session, message->guid, message->headerSize, &messageDateUtc)) == STATUS_CONTINUE) {
        *found = ((messageDateUtc >= date) && (messageDateUtc < date + (60 * 60 * 24)));
        return(STATUS_CONTINUE);
    }
    return(ccode);
}

static long
SearchRemainingMatchSentSince(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    long ccode;
    time_t date = *(unsigned long *)arg2;
    time_t messageDateUtc = 0;

    if ((ccode = GetMessageSentDate(session, message->guid, message->headerSize, &messageDateUtc)) == STATUS_CONTINUE) {
        *found = (messageDateUtc >= date);
        return(STATUS_CONTINUE);
    }
    return(ccode);
}

static long
SearchRemainingMatchLarger(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    unsigned long size = (unsigned long)arg2;

    *found = (message->size > size);
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchSmaller(ImapSession *session, MessageInformation *message, char *arg1, void *arg2, BOOL *found)
{
    unsigned long size = (unsigned long)arg2;

    *found = (message->size < size);
    return(STATUS_CONTINUE);
}

static long
SearchRemainingMatchSubstring(ImapSession *session, MessageInformation *message, char *subcommand, void *arg2, BOOL *found)
{
    char *searchString = (char *)arg2;
    long ccode;

    if (NMAPSendCommandF(session->store.conn, "SEARCH %llx %s \"%s\"\r\n", message->guid, subcommand, searchString) != -1) {
        if ((ccode = NMAPReadResponse(session->store.conn, session->store.response, sizeof(session->store.response), TRUE)) == 1000) {
            *found = FALSE;
            return(STATUS_CONTINUE);
        }

        if (ccode == 2001) {
            *found = TRUE;
            return(CheckStoreResponseCode(1000, NMAPReadResponse(session->store.conn, NULL, 0, 0)));
        }
        return(ccode);
    }
    return(STATUS_NMAP_COMM_ERROR);
}


__inline static long
SearchRemainingMatches(ImapSession *session, SearchKey *key, SearchRemainingMatch searchRemainingFunction, char *arg1, void *arg2)
{
    MessageInformation *message = &session->folder.selected.message[0];    
    long ccode;
    long readNum = 0;
    long writeNum = 0;
    BOOL match;

//XplConsolePrintf("SearchRemaingMatches\n");
    for (;;) {
        if (key->matchList[readNum] != -1) {
//            XplConsolePrintf("\t%d:%d:%lu", key->matchList[readNum], key->matchList[readNum] + 1, message[key->matchList[readNum]].uid);
            if ((ccode = searchRemainingFunction(session, &message[key->matchList[readNum]], arg1, arg2, &match)) == STATUS_CONTINUE) {
                if (match) {
//                    XplConsolePrintf(" match!");
                    key->matchList[writeNum] = key->matchList[readNum];
                    writeNum++;
                }
//XplConsolePrintf("\n");
                readNum++;
                continue;
            }
//XplConsolePrintf(" error %d\n", ccode);
            return(ccode);
        }
        break;
    }

    if (writeNum < readNum) {
        key->matchList[writeNum] = -1;
        key->matchDropped = TRUE;
    }
    return(STATUS_CONTINUE);
}

/* SearchRemainingMatch end */


__inline static long
MessageSetAnd(ImapSession *session, SearchKey *key, long *messageSet)
{
    long readNum = 0;
    long writeNum = 0;

    for (;;) {
        if (key->matchList[readNum] != -1) {
            if (messageSet[key->matchList[readNum]]) {
                key->matchList[writeNum] = key->matchList[readNum];
                writeNum++;
            }
            readNum++;
            continue;
        }
        break;
    }

    if (writeNum < readNum) {
        key->matchList[writeNum] = -1;
        key->matchDropped = TRUE;
    }
    return(STATUS_CONTINUE);
}



__inline static long
SubStringSearch(ImapSession *session, char **keyString, SearchKey *key, char *subcommand)
{
    long ccode;
    char *utf8SearchString;
    
    if ((ccode = GetSearchStringArg(session, key, keyString, &utf8SearchString)) == STATUS_CONTINUE) {
        if (key->matchDropped) {
            ccode = SearchRemainingMatches(session, key, SearchRemainingMatchSubstring, subcommand, utf8SearchString);
        } else {
            ccode = FullListSubstringSearch(session, key, utf8SearchString, subcommand);
        }
        MemFree(utf8SearchString);
    }
    
    return(ccode);    
}


/* key handlers begin */

static long
SearchKeyHandlerAll(ImapSession *session, char **keyString, SearchKey *key)
{
    return(STATUS_CONTINUE);
}

static long
SearchKeyHandlerAnswered(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlag, NULL, (void *)STORE_MSG_FLAG_ANSWERED));
}

static long
SearchKeyHandlerBcc(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;
    char *utf8SearchString;

    if ((ccode = GetSearchStringArg(session, key, keyString, &utf8SearchString)) == STATUS_CONTINUE) {
        MemFree(utf8SearchString);
        /* The bcc field does not exist to search */
        return(FullListNoMatch(key));
    }
    return(ccode);
}

static long
SearchKeyHandlerBefore(ImapSession *session, char **keyString, SearchKey *key)
{
    time_t date;
    long ccode;
        
    if ((ccode = GetDateArg(session, keyString, &date)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchBefore, NULL, &date);
    }
    return(ccode);
}


static long
SearchKeyHandlerBody(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SubStringSearch(session, keyString, key, "BODY"));
}

static long
SearchKeyHandlerCc(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SubStringSearch(session, keyString, key, "HEADER CC"));
}

static long
SearchKeyHandlerDeleted(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlag, NULL, (void *)STORE_MSG_FLAG_DELETED));
}

static long
SearchKeyHandlerDraft(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlag, NULL, (void *)STORE_MSG_FLAG_DRAFT));
}

static long
SearchKeyHandlerFlagged(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlag, NULL, (void *)STORE_MSG_FLAG_FLAGGED));
}

static long
SearchKeyHandlerFrom(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SubStringSearch(session, keyString, key, "HEADER FROM"));
}

static long
SearchKeyHandlerHeader(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;
    unsigned char *headerName;
    char substring[SEARCH_MAX_UTF8_STRING];

    if((ccode = GrabArgumentEx(session, (unsigned char **)keyString, &headerName, TRUE)) == STATUS_CONTINUE) {
        snprintf(substring, sizeof(substring), "HEADER %s", headerName);
        MemFree(headerName);
        return(SubStringSearch(session, keyString, key, substring));
    }
    return(ccode);
}

static long
SearchKeyHandlerKeyword(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;
    char *utf8SearchString;

    if ((ccode = GetSearchStringArg(session, key, keyString, &utf8SearchString)) == STATUS_CONTINUE) {
        MemFree(utf8SearchString);
        /* this implementation does not support keywords so no message will match */
        return(FullListNoMatch(key));
    }
    return(ccode);
}

static long
SearchKeyHandlerLarger(ImapSession *session, char **keyString, SearchKey *key)
{
    unsigned long size;
    long ccode;
        
    if ((ccode = GetNumericArg(session, keyString, &size)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchLarger, NULL, (void *)size);
    }
    return(ccode);
}

static long
SearchKeyHandlerNew(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchNew, NULL, NULL));
}

static long
SearchKeyHandlerNot(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;
    SearchKey newKey;

    if ((ccode = SearchKeyObjectDuplicate(key, &newKey)) == STATUS_CONTINUE) {
        if ((ccode = SearchHandleKey(session, keyString, &newKey)) == STATUS_CONTINUE) {
            MarkMatches(key, &newKey);
            MarkedMatchesRemove(key);
        }
        SearchKeyObjectFree(&newKey);
    }
    return(ccode);
}

static long
SearchKeyHandlerOld(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlagNot, NULL, (void *)STORE_MSG_FLAG_RECENT));
}

static long
SearchKeyHandlerOn(ImapSession *session, char **keyString, SearchKey *key)
{
    time_t date;
    long ccode;
        
    if ((ccode = GetDateArg(session, keyString, &date)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchOn, NULL, &date);
    }
    return(ccode);
}

static long
SearchKeyHandlerOr(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;
    SearchKey leftKey;
    SearchKey rightKey;

    if ((ccode = SearchKeyObjectDuplicate(key, &leftKey)) == STATUS_CONTINUE) {
        if ((ccode = SearchHandleKey(session, keyString, &leftKey)) == STATUS_CONTINUE) {

            ccode = STATUS_INVALID_ARGUMENT; 
            if (**keyString == ' ') {
                (*keyString)++;
                if ((ccode = SearchKeyObjectDuplicate(key, &rightKey)) == STATUS_CONTINUE) {
                    if ((ccode = SearchHandleKey(session, keyString, &rightKey)) == STATUS_CONTINUE) {
                        MarkMatches(key, &leftKey);
                        MarkMatches(key, &rightKey);
                        MarkedMatchesPreserve(key);
                    }
                    SearchKeyObjectFree(&rightKey);
                }
            }
        }
        SearchKeyObjectFree(&leftKey);
    }
    return(ccode);
}

static long
SearchKeyHandlerRecent(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlag, NULL, (void *)STORE_MSG_FLAG_RECENT));
}

static long
SearchKeyHandlerSeen(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlag, NULL, (void *)STORE_MSG_FLAG_SEEN));
}

static long
SearchKeyHandlerSentBefore(ImapSession *session, char **keyString, SearchKey *key)
{
    time_t date;
    long ccode;
        
    if ((ccode = GetDateArg(session, keyString, &date)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchSentBefore, NULL, &date);
    }
    return(ccode);
}

static long
SearchKeyHandlerSentOn(ImapSession *session, char **keyString, SearchKey *key)
{
    time_t date;
    long ccode;
        
    if ((ccode = GetDateArg(session, keyString, &date)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchSentOn, NULL, &date);
    }
    return(ccode);
}

static long
SearchKeyHandlerSentSince(ImapSession *session, char **keyString, SearchKey *key)
{
    time_t date;
    long ccode;
        
    if ((ccode = GetDateArg(session, keyString, &date)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchSentSince, NULL, &date);
    }
    return(ccode);
}

static long
SearchKeyHandlerSince(ImapSession *session, char **keyString, SearchKey *key)
{
    time_t date;
    long ccode;
        
    if ((ccode = GetDateArg(session, keyString, &date)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchSince, NULL, &date);
    }
    return(ccode);
}

static long
SearchKeyHandlerSmaller(ImapSession *session, char **keyString, SearchKey *key)
{
    unsigned long size;
    long ccode;
        
    if ((ccode = GetNumericArg(session, keyString, &size)) == STATUS_CONTINUE) {
        ccode = SearchRemainingMatches(session, key, SearchRemainingMatchSmaller, NULL, (void *)size);
    }
    return(ccode);
}

static long
SearchKeyHandlerSubject(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SubStringSearch(session, keyString, key, "HEADER SUBJECT"));
}

static long
SearchKeyHandlerText(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SubStringSearch(session, keyString, key, "TEXT"));
}

static long
SearchKeyHandlerTo(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SubStringSearch(session, keyString, key, "HEADER TO"));
}

static long
SearchKeyHandlerUid(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;
    long *messageSet;

    if ((ccode = GetMessageSetArg(session, keyString, &messageSet)) == STATUS_CONTINUE) {
        ccode = MessageSetAnd(session, key, messageSet);
        MemFree(messageSet);
    }
    return(ccode);
}

static long
SearchKeyHandlerUnanswered(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlagNot, NULL, (void *)STORE_MSG_FLAG_ANSWERED));
}

static long
SearchKeyHandlerUndeleted(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlagNot, NULL, (void *)STORE_MSG_FLAG_DELETED));
}

static long
SearchKeyHandlerUndraft(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlagNot, NULL, (void *)STORE_MSG_FLAG_DRAFT));
}

static long
SearchKeyHandlerUnflagged(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlagNot, NULL, (void *)STORE_MSG_FLAG_FLAGGED));
}

static long
SearchKeyHandlerUnkeyword(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;
    char *utf8SearchString;

    if ((ccode = GetSearchStringArg(session, key, keyString, &utf8SearchString)) == STATUS_CONTINUE) {
        MemFree(utf8SearchString);
        /* this implementation does not support keywords so all  messages will match */
        return(STATUS_CONTINUE);
    }
    return(ccode);
}

static long
SearchKeyHandlerUnseen(ImapSession *session, char **keyString, SearchKey *key)
{
    return(SearchRemainingMatches(session, key, SearchRemainingMatchFlagNot, NULL, (void *)STORE_MSG_FLAG_SEEN));
}

/* key handlers end */

static SearchKeyInfo SearchKeyList[] = {
    {"ALL", sizeof("ALL") - 1, SearchKeyHandlerAll},
    {"ANSWERED", sizeof("ANSWERED") - 1, SearchKeyHandlerAnswered},
    {"BCC ", sizeof("BCC ") - 1, SearchKeyHandlerBcc},
    {"BEFORE ", sizeof("BEFORE ") - 1, SearchKeyHandlerBefore},
    {"BODY ", sizeof("BODY ") - 1, SearchKeyHandlerBody},
    {"CC ", sizeof("CC ") - 1, SearchKeyHandlerCc},
    {"DELETED", sizeof("DELETED") - 1, SearchKeyHandlerDeleted},
    {"DRAFT", sizeof("DRAFT") - 1, SearchKeyHandlerDraft},
    {"FLAGGED", sizeof("FLAGGED") - 1, SearchKeyHandlerFlagged},
    {"FROM ", sizeof("FROM ") - 1, SearchKeyHandlerFrom},
    {"HEADER ", sizeof("HEADER ") - 1, SearchKeyHandlerHeader},
    {"KEYWORD ", sizeof("KEYWORD ") - 1, SearchKeyHandlerKeyword},
    {"LARGER ", sizeof("LARGER ") - 1, SearchKeyHandlerLarger},
    {"NEW", sizeof("NEW") - 1, SearchKeyHandlerNew},
    {"NOT ", sizeof("NOT ") - 1, SearchKeyHandlerNot},
    {"OLD", sizeof("OLD") - 1, SearchKeyHandlerOld},
    {"ON ", sizeof("ON ") - 1, SearchKeyHandlerOn},
    {"OR ", sizeof("OR ") - 1, SearchKeyHandlerOr},
    {"RECENT", sizeof("RECENT") - 1, SearchKeyHandlerRecent},
    {"SEEN", sizeof("SEEN") - 1, SearchKeyHandlerSeen},
    {"SENTBEFORE ", sizeof("SENTBEFORE ") - 1, SearchKeyHandlerSentBefore},
    {"SENTON ", sizeof("SENTON ") - 1, SearchKeyHandlerSentOn},
    {"SENTSINCE ", sizeof("SENTSINCE ") - 1, SearchKeyHandlerSentSince},
    {"SINCE ", sizeof("SINCE ") - 1, SearchKeyHandlerSince},
    {"SMALLER ", sizeof("SMALLER ") - 1, SearchKeyHandlerSmaller},
    {"SUBJECT ", sizeof("SUBJECT ") - 1, SearchKeyHandlerSubject},
    {"TEXT ", sizeof("TEXT ") - 1, SearchKeyHandlerText},
    {"TO ", sizeof("TO ") - 1, SearchKeyHandlerTo},
    {"UID ", sizeof("UID ") - 1, SearchKeyHandlerUid},
    {"UNANSWERED", sizeof("UNANSWERED") - 1, SearchKeyHandlerUnanswered},
    {"UNDELETED", sizeof("UNDELETED") - 1, SearchKeyHandlerUndeleted},
    {"UNDRAFT", sizeof("UNDRAFT") - 1, SearchKeyHandlerUndraft},
    {"UNFLAGGED", sizeof("UNFLAGGED") - 1, SearchKeyHandlerUnflagged},
    {"UNKEYWORD ", sizeof("UNKEYWORD ") - 1, SearchKeyHandlerUnkeyword},
    {"UNSEEN", sizeof("UNSEEN") - 1, SearchKeyHandlerUnseen},
    {NULL, 0, NULL}
};

long
SearchHandleKey(ImapSession *session, char **keyString, SearchKey *key)
{
    long keyId;

    if (**keyString != '(') {
        if ((keyId = BongoKeywordBegins(SearchKeyIndex, *keyString)) != -1) {
            *keyString += SearchKeyList[keyId].nameLen;
            return(SearchKeyList[keyId].handler(session, (void *)keyString, key));
        }
        return(STATUS_INVALID_ARGUMENT);
    }

    (*keyString)++;
    return(SearchHandleSubKey(session, keyString, key));
}

static long
SearchHandleSubKey(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;

    for(;;) {
        if ((ccode = SearchHandleKey(session, keyString, key)) == STATUS_CONTINUE) {
            if (**keyString == ' ') {
                (*keyString)++;
                continue;
            }

            if (**keyString == ')') {
                (*keyString)++;
                return(STATUS_CONTINUE);
            }
                    
            return(STATUS_INVALID_ARGUMENT);
        }
        return(ccode);
    }
}


static long
SearchHandleTopKey(ImapSession *session, char **keyString, SearchKey *key)
{
    long ccode;

    for(;;) {
        if ((ccode = SearchHandleKey(session, keyString, key)) == STATUS_CONTINUE) {
            if (**keyString == ' ') {
                (*keyString)++;
                continue;
            }

            if (!**keyString) {
                return(STATUS_CONTINUE);
            }
                    
            return(STATUS_INVALID_ARGUMENT);
        }
        return(ccode);
    }
}

__inline static long
SearchSendResults(ImapSession *session, long *matches, BOOL byUid)
{
    MessageInformation *message = NULL;
    if (byUid) {
        message = &session->folder.selected.message[0];
    } else {
        // FIXME - What should we do here?
    }

    if (ConnWrite(session->client.conn, "* SEARCH", strlen("* SEARCH")) != -1) {
        unsigned long i = 0;

        for (;;) {
            if (matches[i] != -1) {
                if (byUid) {
                    if (ConnWriteF(session->client.conn, " %lu", (unsigned long)message[matches[i]].uid) != -1) {
                        i++;
                        continue;
                    }
                    return(STATUS_ABORT);
                }

                if (ConnWriteF(session->client.conn, " %lu", matches[i] + 1) != -1) {
                    i++;
                    continue;
                }
                return(STATUS_ABORT);
            }
            break;
        }

        if (ConnWrite(session->client.conn, "\r\n", strlen("\r\n")) != -1) {
            return(STATUS_CONTINUE);
        }
    }
    return(STATUS_ABORT);
}


static long
SearchHandleCommand(ImapSession *session, char *parameters, BOOL byUid)
{
    long ccode;
    char *ptr = parameters;
    char *charsetPtr;
    SearchKey key;

    if ((ccode = CheckState(session, STATE_SELECTED)) == STATUS_CONTINUE) {
        /* rfc 2180 discourages purge notifications during the search command */
        if ((ccode = EventsSend(session, STORE_EVENT_NEW | STORE_EVENT_FLAG)) == STATUS_CONTINUE) {
            if (XplStrNCaseCmp(ptr, "CHARSET ", strlen("CHARSET ")) != 0) {
                ccode = SearchKeyObjectCreate(&key, "utf-8", session->folder.selected.messageCount);
                /* the spec says us-ascii should be the default, */
                /* but since us-ascii is a subset of utf-8, why not */
            } else {
                ptr += strlen("CHARSET ");
                charsetPtr = ptr;
                ptr = strchr(ptr, ' ');
                if (!ptr) {
                    return(STATUS_INVALID_ARGUMENT);
                }
                
                *ptr = '\0';
                ccode = SearchKeyObjectCreate(&key, charsetPtr, session->folder.selected.messageCount);
                *ptr = ' ';
                ptr++;
            }

            if (ccode == STATUS_CONTINUE) {
                if ((ccode = SearchHandleTopKey(session, &ptr, &key)) == STATUS_CONTINUE) {
                    ccode = SearchSendResults(session, key.matchList, byUid);
                }
                SearchKeyObjectFree(&key);
            }
        }
    }

    return(ccode);
}    

int
ImapCommandSearch(void *param)
{
    long ccode;

    ImapSession *session = (ImapSession *)param;

    if ((ccode = SearchHandleCommand(session, session->command.buffer + strlen("SEARCH "), FALSE)) == STATUS_CONTINUE) {
        return(SendOk(session, "SEARCH"));
    }

    return(SendError(session->client.conn, session->command.tag, "SEARCH", ccode));
}

int
ImapCommandUidSearch(void *param)
{
    long ccode;

    ImapSession *session = (ImapSession *)param;

    if ((ccode = SearchHandleCommand(session, session->command.buffer + strlen("UID SEARCH "), TRUE)) == STATUS_CONTINUE) {
        return(SendOk(session, "UID SEARCH"));
    }

    return(SendError(session->client.conn, session->command.tag, "UID SEARCH", ccode));
}


void
CommandSearchCleanup(void)
{
    BongoKeywordIndexFree(SearchKeyIndex);
}

BOOL
CommandSearchInit(void)
{
    BongoKeywordIndexCreateFromTable(SearchKeyIndex, SearchKeyList, .name, TRUE); 
    if (SearchKeyIndex != NULL) {
        return(TRUE);
    } 
    return(FALSE);
}
