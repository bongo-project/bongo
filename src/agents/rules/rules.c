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

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <streamio.h>
#include <connio.h>
#include <rulesrv.h>

#include "rules.h"

#if defined(RELEASE_BUILD)
#define RulesClientAlloc() MemPrivatePoolGetEntry(Rules.nmap.pool)
#else
#define RulesClientAlloc() MemPrivatePoolGetEntryDebug(Rules.nmap.pool, __FILE__, __LINE__)
#endif

static void SignalHandler(int sigtype);

#define QUEUE_WORK_TO_DO(c, id, r) \
        { \
            XplWaitOnLocalSemaphore(Rules.nmap.semaphore); \
            if (XplSafeRead(Rules.nmap.worker.idle)) { \
                (c)->queue.previous = NULL; \
                if (((c)->queue.next = Rules.nmap.worker.head) != NULL) { \
                    (c)->queue.next->queue.previous = (c); \
                } else { \
                    Rules.nmap.worker.tail = (c); \
                } \
                Rules.nmap.worker.head = (c); \
                (r) = 0; \
            } else { \
                XplSafeIncrement(Rules.nmap.worker.active); \
                XplSignalBlock(); \
                XplBeginThread(&(id), HandleConnection, 24 * 1024, XPL_INT_TO_PTR(XplSafeRead(Rules.nmap.worker.active)), (r)); \
                XplSignalHandler(SignalHandler); \
                if (!(r)) { \
                    (c)->queue.previous = NULL; \
                    if (((c)->queue.next = Rules.nmap.worker.head) != NULL) { \
                        (c)->queue.next->queue.previous = (c); \
                    } else { \
                        Rules.nmap.worker.tail = (c); \
                    } \
                    Rules.nmap.worker.head = (c); \
                } else { \
                    XplSafeDecrement(Rules.nmap.worker.active); \
                    (r) = -1; \
                } \
            } \
            XplSignalLocalSemaphore(Rules.nmap.semaphore); \
        }


RulesGlobals Rules;

static BOOL 
RulesClientAllocCB(void *buffer, void *data)
{
    register RulesClient *c = (RulesClient *)buffer;

    memset(c, 0, sizeof(RulesClient));

    return(TRUE);
}

static void 
RulesClientFree(RulesClient *client)
{
    register RulesClient *c = client;

    if (c->conn) {
        ConnClose(c->conn, 1);
        ConnFree(c->conn);
        c->conn = NULL;
    }

    MemPrivatePoolReturnEntry(c);

    return;
}

static void 
FreeBongoRule(BongoRule *rule)
{
    BongoRuleCondition *c;
    BongoRuleAction *a;
    BongoRule *r = rule;
    void *next;

    if (r) {
        c = r->conditions.head;
        while (c) {
            next = (void *)c->next;
            MemFree(c);

            c = (BongoRuleCondition *)next;
        }

        a = r->actions.head;
        while (a) {
            next = (void *)a->next;
            MemFree(a);

            a = (BongoRuleAction *)next;
        }

        MemFree(r);
    }

    return;
}

static void 
FreeClientData(RulesClient *client)
{
    if (client && !(client->flags & RULES_CLIENT_FLAG_EXITING)) {
        client->flags |= RULES_CLIENT_FLAG_EXITING;

        if (client->conn) {
            ConnClose(client->conn, 1);
            ConnFree(client->conn);
            client->conn = NULL;
        }

        if (client->store) {
            NMAPQuit(client->store);
            ConnFree(client->store);
            client->store = NULL;
        }

#if CAL_IMPLEMENTED
        if (client->iCal.data) {
            MemFree(client->iCal.data);
            client->iCal.data = NULL;
        }

        if (client->iCal.object) {
            ICalFreeObjects(client->iCal.object);
            client->iCal.object = NULL;
        }
#endif

        if (client->rules.head) {
            FreeBongoRule(client->rules.head);
            client->rules.head = NULL;
            client->rules.tail = NULL;
        }

        if (client->mime) {
            MDBDestroyValueStruct(client->mime);
            client->mime = NULL;
        }

        if (client->envelope) {
            MemFree(client->envelope);
            client->envelope = NULL;
        }
    }

    return;
}

static Connection *
ConnectToUserNMAPStore(RulesClient *client)
{
    int ccode;
    BOOL result;
    Connection *conn;

    if ((conn = NMAPConnectEx(NULL, &(client->nmap), client->conn->trace.destination)) != NULL) {
        result = NMAPAuthenticate(conn, client->line, CONN_BUFSIZE);
    } else {
        LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE, LOG_ERROR, 0, NULL, NULL, client->nmap.sin_addr.s_addr, 0, NULL, 0);
        return(NULL);
    }

    if (result 
            && ((ccode = ConnWriteF(conn, "USER %s\r\n", client->user)) != -1) 
            && ((ccode = ConnFlush(conn)) != -1) 
            && ((ccode = NMAPReadAnswer(conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
        return(conn);
    }

    NMAPQuit(conn);
    conn = NULL;

    return(NULL);
}

static int 
CompareRuleID(const void *first, const void *second)
{
    unsigned long oneID;
    unsigned long twoID;
    unsigned char temp;
    const unsigned char *one = *(unsigned char **)first;
    const unsigned char *two = *(unsigned char **)second;

    if (one && two && (strlen((unsigned char *)one) > 9) && (strlen((unsigned char *)two) > 9)) {
        temp = ((unsigned char *)one)[8];
        ((unsigned char *)one)[8] = '\0';
        oneID = strtol((unsigned char *)one, NULL, 16);
        ((unsigned char *)one)[8] = temp;

        temp = ((unsigned char *)two)[8];
        ((unsigned char *)two)[8] = '\0';
        twoID = strtol((unsigned char *)two, NULL, 16);
        ((unsigned char *)two)[8] = temp;

        if (oneID < twoID) {
            return(-1);
        } else if (oneID > twoID) {
            return(1);
        }
    }

    return(0);
}

/* The format of a rules string is:

    <UniqueID><Active><Condition><Length Arg1><Arg1><Length Arg2><Arg2><Action><Length Arg1><Arg1><Length Arg2><Arg2><Bool>

    <UniqueID>
        8 byte Digits w/leading zeros

    <Active>
        1 byte ASCII char
            'A' RULE_ACTIVE
            'B' RULE_INACTIVE
    <Condition>
        1 byte ASCII char
            'A' RULE_COND_ANY
            'B' RULE_COND_FROM
            'C' RULE_COND_TO
            'D' RULE_COND_SUBJECT
            'E' RULE_COND_PRIORITY
            'F' RULE_COND_HEADER
            'G' RULE_COND_BODY
            'H' RULE_COND_FROM_NOT
            'I' RULE_COND_TO_NOT
            'J' RULE_COND_SUBJECT_NOT
            'K' RULE_COND_PRIORITY_NOT
            'L' RULE_COND_HEADER_NOT
            'M' RULE_COND_BODY_NOT
            'N' RULE_COND_SIZE_MORE
            'O' RULE_COND_SIZE_LESS

            'P' RULE_COND_TIMEOFDAYLESS
            'Q' RULE_COND_TIMEOFDAYMORE
            'R' RULE_COND_WEEKDAYIS
            'S' RULE_COND_MONTHDAYIS
            'T' RULE_COND_MONTHDAYISNOT
            'U' RULE_COND_MONTHDAYLESS
            'a' RULE_COND_MONTHDAYMORE
            'b' RULE_COND_MONTHIS
            'c' RULE_COND_MONTHISNOT
            'd' RULE_COND_FREE
            'e' RULE_COND_FREENOT

            'f' RULE_COND_HASMIMETYPE
            'g' RULE_COND_HASMIMETYPENOT

    <Action>
        1 byte ASCII char
            'A' RULE_ACT_REPLY
            'B' RULE_ACT_DELETE
            'C' RULE_ACT_FORWARD
            'D' RULE_ACT_COPY
            'E' RULE_ACT_MOVE
            'F' RULE_ACT_STOP

            'G' RULE_ACT_ACCEPT
            'H' RULE_ACT_DECLINE
            'I' RULE_ACT_BOUNCE

    <Bool>
        1 byte ASCII char

            'V' RULE_ACT_BOOL_START
            'W' RULE_ACT_BOOL_NONE
            'X' RULE_ACT_BOOL_AND
            'Y' RULE_ACT_BOOL_OR
            'Z' RULE_ACT_BOOL_NOT

    <Length Arg1>, <Length Arg2>
        3 digits with leading zeros

    <Arg1>, <Arg2>
        Variable length string, 'Z' (ASCII 90) terminator, length does not include terminator.
        Always terminated, even if empty

    The shortest legitamate rule will have at least 27 characters and would be:
        <UniqueID>AA000Z000ZB000Z000Z

    If from contains annaleis or if from contains asmith then move to Bongo/Commits
        <UniqueID>AB008annaleisZ000ZYB006asmithZ000ZE012Bongo/CommitsZ000Z

    If subject contains Already and if body contains Already or if body contains Never then move to Junk E-mail
        <UniqueID>AD007AlreadyZ000ZXG007AlreadyZ000ZYG005NeverZ000ZE011JunkE-mailZ00Z

    If from doesn't contain dmsmith or if from contains dmsmith then move to INBOX and stop processing
        <UniqueID>AH007dmsmithZ000ZYB007dmsmithZ000ZE005INBOXZ000ZF000Z000Z

    If from contains brandon or if from contains tyler or if from contains julie then move to Bongo and then carbon copy to dmsmith@novell.com and stop processing
        <UniqueID>AB007brandonZ000ZYB005tylerZ000ZYB005julieZ000ZE004BongoZ000ZD018dmsmith@novell.comZ000ZF000Z000Z
*/
static void 
ParseRules(RulesClient *client, MDBValueStruct *vs)
{
    unsigned char *cur;
    unsigned char *limit;
    unsigned long used;
    BOOL parsed = FALSE;
    BongoRuleAction *action = NULL;
    BongoRuleCondition *condition = NULL;
    BongoRule *rule = NULL;

    qsort(vs->Value, vs->Used, sizeof(unsigned char *), CompareRuleID);

    for (used = 0; used < vs->Used; used++) {
        cur = vs->Value[used];
        limit = vs->Value[used] + strlen(vs->Value[used]);

        if ((limit > cur) && ((limit - cur) >= 27)) {
            if (cur[8] == RULE_ACTIVE) {
                parsed = TRUE;

                rule = (BongoRule *)MemMalloc(sizeof(BongoRule));
            } else {
                continue;
            }
        } else {
            XplConsolePrintf("bongorules: [%d] User %s rule %lu is truncated! %s\r\n", __LINE__, client->dn, used, vs->Value[used]);

            continue;
        }

        if (rule) {
            memset(rule, 0, sizeof(BongoRule));

            rule->active = TRUE;
            rule->string = vs->Value[used];
            memcpy(rule->id, rule->string, 8);
        } else {
            LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(BongoRule), __LINE__, NULL, 0);

            parsed = FALSE;
            break;
        }

        cur += 9;
        while (parsed && (cur < limit)) {
            if (!condition) {
                condition = (BongoRuleCondition *)MemMalloc(sizeof(BongoRuleCondition));
                if (condition) {
                    memset(condition, 0, sizeof(BongoRuleCondition));
                } else {
                    LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(BongoRuleCondition), __LINE__, NULL, 0);

                    parsed = FALSE;
                    break;
                }
            }

            switch (*cur) {
                case RULE_COND_ANY:
                case RULE_COND_FREE: 
                case RULE_COND_FREENOT: {
                    /* no arguments */
                    if (memcmp(cur + 1, "000Z000Z", 8) == 0) {
                        condition->type = *cur;

                        cur += 9;

                        if (cur < limit) {
                            break;
                        }
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_FROM: 
                case RULE_COND_TO: 
                case RULE_COND_SUBJECT: 
                case RULE_COND_PRIORITY: 
                case RULE_COND_BODY: 
                case RULE_COND_FROM_NOT: 
                case RULE_COND_TO_NOT: 
                case RULE_COND_SUBJECT_NOT: 
                case RULE_COND_PRIORITY_NOT: 
                case RULE_COND_BODY_NOT: 
                case RULE_COND_HASMIMETYPE: 
                case RULE_COND_HASMIMETYPENOT: {
                    /* sole argument is a search string */
                    condition->type = *cur;
                    condition->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    condition->string[0] = cur;

                    cur += condition->length[0];
                    if (memcmp(cur, "Z000Z", 5) == 0) {
                        cur[0] = '\0';
                        cur += 5;

                        if (cur < limit) {
                            break;
                        }
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_HEADER: 
                case RULE_COND_HEADER_NOT: {
                    /* first argument is the header field; second argument is the search string */
                    condition->type = *cur;
                    condition->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    condition->string[0] = cur;

                    cur += condition->length[0];
                    if ((cur < (limit - 5)) && (*cur == 'Z')) {
                        *cur++ = '\0';

                        condition->length[1] = ((cur[0] - '0') * 100) + ((cur[1] - '0') * 10) + (cur[2] - '0');

                        cur += 3;
                        condition->string[1] = cur;

                        cur += condition->length[1];
                        if ((cur < (limit - 1)) && (*cur == 'Z')) {
                            *cur++ = '\0';

                            break;
                        }
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_SIZE_MORE: 
                case RULE_COND_SIZE_LESS: {
                    /* sole argument is a size in kilobytes */
                    condition->type = *cur;
                    condition->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    condition->string[0] = cur;

                    cur += condition->length[0];
                    if ((cur < (limit - 5)) && (*cur == 'Z') && (memcmp(cur + 1, "000Z", 4) == 0)) {
                        cur[0] = '\0';
                        cur += 5;

                        condition->count[0] = atol(condition->string[0]) * 1024;

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_TIMEOFDAYLESS: 
                case RULE_COND_TIMEOFDAYMORE: {
                    /* sole argument is a count in seconds */
                    condition->type = *cur;
                    condition->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    condition->string[0] = cur;

                    cur += condition->length[0];
                    if ((cur < (limit - 5)) && (*cur == 'Z') && (memcmp(cur + 1, "000Z", 4) == 0)) {
                        cur[0] = '\0';
                        cur += 5;

                        condition->count[0] = atol(condition->string[0]);

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_WEEKDAYIS: {
                    /* sole argument is a numerical day of week; 0 = sunday */
                    condition->type = *cur;
                    condition->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    condition->string[0] = cur;

                    cur += condition->length[0];
                    if ((cur < (limit - 5)) 
                            && (memcmp(cur, "Z000Z", 5) == 0) 
                            && (condition->string[0][0] >= '0')  
                            && (condition->string[0][0] <= '6')) {
                        cur[0] = '\0';
                        cur += 5;

                        condition->count[0] = condition->string[0][0] - '0';

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_MONTHDAYIS: 
                case RULE_COND_MONTHDAYISNOT: 
                case RULE_COND_MONTHDAYLESS: 
                case RULE_COND_MONTHDAYMORE: {
                    /* sole argument is a day of month */
                    condition->type = *cur;
                    condition->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    condition->string[0] = cur;

                    cur += condition->length[0];
                    if ((cur < (limit - 5)) 
                            && (*cur == 'Z') 
                            && (memcmp(cur + 1, "000Z", 4) == 0) 
                            && (atol(condition->string[0]) >= 0) 
                            && (atol(condition->string[0]) <= 31)) {
                        cur[0] = '\0';
                        cur += 5;

                        condition->count[0] = atol(condition->string[0]);

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_MONTHIS: 
                case RULE_COND_MONTHISNOT: {
                    /* sole argument is a month */
                    condition->type = *cur;
                    condition->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    condition->string[0] = cur;

                    cur += condition->length[0];
                    if ((cur < (limit - 5)) 
                            && (*cur == 'Z') 
                            && (memcmp(cur + 1, "000Z", 4) == 0) 
                            && (atol(condition->string[0]) >= 0) 
                            && (atol(condition->string[0]) <= 12)) {
                        cur[0] = '\0';
                        cur += 5;

                        condition->count[0] = atol(condition->string[0]);

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                default: {
                    parsed = FALSE;
                    break;
                }
            }

            if (parsed) {
                switch (*cur) {
                    case RULE_ACT_BOOL_AND: 
                    case RULE_ACT_BOOL_OR: 
                    case RULE_ACT_BOOL_NOT: {
                        condition->operand = *cur++;

                        RULE_APPEND_CONDITION(rule, condition);

                        continue;
                    }

                    default: {
                        RULE_APPEND_CONDITION(rule, condition);

                        break;
                    }
                }
            }

            break;
        }

        while (parsed && (cur < limit)) {
            if (!action) {
                action = (BongoRuleAction *)MemMalloc(sizeof(BongoRuleAction));
                if (action) {
                    memset(action, 0, sizeof(BongoRuleAction));
                } else {
                    LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(BongoRuleAction), __LINE__, NULL, 0);

                    parsed = FALSE;
                    break;
                }
            }

            switch (*cur) {
                case RULE_ACT_REPLY: 
                case RULE_ACT_FORWARD: 
                case RULE_ACT_COPY: 
                case RULE_ACT_MOVE: {
                    /* sole argument is a filename? (reply), a remote address (forward and copy), or a mailbox (move) */
                    action->type = *cur;
                    action->length[0] = ((cur[1] - '0') * 100) + ((cur[2] - '0') * 10) + (cur[3] - '0');

                    cur += 4;
                    action->string[0] = cur;

                    cur += action->length[0];
                    if (memcmp(cur, "Z000Z", 5) == 0) {
                        cur[0] = '\0';
                        cur += 5;

                        if (cur <= limit) {
                            RULE_APPEND_ACTION(rule, action);

                            continue;
                        }
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_ACT_DELETE: 
                case RULE_ACT_STOP: 
                case RULE_ACT_ACCEPT: 
                case RULE_ACT_DECLINE: 
                case RULE_ACT_BOUNCE: {
                    /* no arguments */
                    if (memcmp(cur + 1, "000Z000Z", 8) == 0) {
                        action->type = *cur;

                        cur += 9;

                        if (cur <= limit) {
                            RULE_APPEND_ACTION(rule, action);

                            continue;
                        }
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_ACT_BOOL_START: 
                case RULE_ACT_BOOL_NONE: 
                case RULE_ACT_BOOL_AND: 
                case RULE_ACT_BOOL_OR: 
                case RULE_ACT_BOOL_NOT: 
                default: {
                    parsed = FALSE;
                    break;
                }
            }
        }

        if (parsed) {
            RULE_APPEND(client, rule);

            continue;
        }

        XplConsolePrintf("bongorules:  User %s rule %lu cannot be parsed! Offset %lu\r\n", client->dn, used, cur - vs->Value[used]);

        if (condition) {
            MemFree(condition);
            condition = NULL;
        }

        if (action) {
            MemFree(action);
            action = NULL;
        }

        FreeBongoRule(rule);
        rule = NULL;
    }

    return;
}

#if CAL_IMPLEMENTED
static ICalObject
*GetCalendarData(RulesClient *client)
{
    int ccode;
    unsigned long used;
    unsigned long start;
    unsigned long size;
    unsigned long response;
    unsigned long depth = 0;
    unsigned char *mime;
    unsigned char *ptr;
    unsigned char *copy;
    unsigned char encoding[64];
    StreamStruct netCodec;
    StreamStruct encodingCodec;
    StreamStruct memoryCodec;

    for (used = 0; used < client->mime->Used; used++) {
        mime = client->mime->Value[used];
        response = atol(mime);

        if (response == 2002) {
            if ((XplStrNCaseCmp(mime + 5, "multipart ", 10) == 0) 
                    || (XplStrNCaseCmp(mime + 5, "message ", 8) == 0)) {
                depth++;
                continue;
            }
        } else if (response == 2003 || response == 2004) {
            if (depth > 0) {
                depth--;
            }

            continue;
        }

        if ((depth < 2) && (strncmp(mime + 5, "text calendar ", 14) == 0)) {
            ptr = strrchr(mime, '\"');                                            
            if (ptr) {
                ptr++;
                sscanf(mime, "%*u %*s %*s %s %*s %*s", encoding);
                sscanf(ptr, " %lu %lu %*u", &start, &size);
            } else {
                sscanf(mime, "%*u %*s %*s %s %*s %*s %lu %lu %*u", encoding, &start, &size);
            }

            if ((XplStrCaseCmp(encoding, "quoted-printable") == 0) && (XplStrCaseCmp(encoding, "base64") == 0)) {
                client->iCal.data = MemMalloc(size + 1);
                if (client->iCal.data != NULL) {
                    client->iCal.length = size;
                    if (((ccode = NMAPSendCommandF(client->conn, "QBRAW %s %lu %lu\r\n", client->queueID, start, size)) != -1) 
                            && (((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 2021) || (ccode == 2025)) 
                            && ((ccode = NMAPReadCount(client->conn, client->iCal.data, size)) != -1)) {
                        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
                    }
                } else {
                    client->iCal.length = 0;
                    
                    LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, size + 1, __LINE__, NULL, 0);
                    break;
                }
            } else {
                memset(&netCodec, 0, sizeof(StreamStruct));
                memset(&encodingCodec, 0, sizeof(StreamStruct));
                memset(&memoryCodec, 0, sizeof(StreamStruct));

                netCodec.Codec = RulesStreamFromNMAP;
                netCodec.StreamData = (void *)client;
                netCodec.Next = &encodingCodec;

                encodingCodec.Codec = FindCodec(encoding, FALSE);
                encodingCodec.Next = &memoryCodec;
                encodingCodec.Start = MemMalloc(sizeof(unsigned char) * CONN_BUFSIZE * 2);
                if (encodingCodec.Start) {
                    encodingCodec.End = encodingCodec.Start + (sizeof(unsigned char) * CONN_BUFSIZE);

                    memoryCodec.Codec = RulesStreamToMemory;
                    memoryCodec.Start = encodingCodec.End;
                    memoryCodec.End = memoryCodec.Start + (sizeof(unsigned char) * CONN_BUFSIZE);

                    if (((ccode = NMAPSendCommandF(client->conn, "QBRAW %s %lu %lu\r\n", client->queueID, start, size)) != -1) 
                            && (((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 2021) || (ccode == 2025))) {
                        netCodec.StreamLength = atol(client->line);

                        netCodec.Codec(&netCodec, netCodec.Next);

                        MemFree(encodingCodec.Start);

                        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
 
                        if (memoryCodec.StreamData) {
                            client->iCal.data = memoryCodec.StreamData;
                            client->iCal.length = memoryCodec.StreamLength;
                        } else {
                            ccode = -1;
                            break;
                        }

                    } else {
                        MemFree(encodingCodec.Start);
                        break;
                    }
                } else {
                    LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(unsigned char) * CONN_BUFSIZE * 2, __LINE__, NULL, 0);
                    break;
                }
            }

            if (client->iCal.data && ((copy = MemMalloc(client->iCal.length + 1)) != NULL)) {
                client->iCal.data[client->iCal.length] = '\0';

                memcpy(copy, client->iCal.data, client->iCal.length);
                client->iCal.object = ICalParseObject(NULL, copy, client->iCal.length);

                MemFree(copy);

                return(client->iCal.object);
            }

            if (client->iCal.data) {
                MemFree(client->iCal.data);
                client->iCal.data = NULL;
            }
        }
    }

    return(NULL);
}
#endif

static BOOL 
IsNMAPShare(RulesClient *client, unsigned char *name, unsigned char *type, unsigned char **owner, unsigned char **trueName, unsigned long *permissions)
{
    unsigned int count;
    unsigned int length;
    unsigned long used;
    unsigned char *ptr;
    unsigned char *displayName;
    unsigned char *pattern;
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char configDn[MDB_MAX_OBJECT_CHARS + 1];
    BOOL result = FALSE;
    MDBValueStruct *vs;

    pattern = (unsigned char *)MemMalloc(XPL_MAX_PATH + MAXEMAILNAMESIZE + 16);
    if (pattern) {
        vs = MDBCreateValueStruct(Rules.handle.directory, NULL);
    } else {
        LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, XPL_MAX_PATH + MAXEMAILNAMESIZE + 16, __LINE__, NULL, 0);
        return(FALSE);
    }

    if (vs) {
        memcpy(pattern, type, 2);
        pattern[2] = '\0';

        MDBFreeValues(vs);

	// FIXME
        // MsgGetUserSetting(client->dn, MSGSRV_A_AVAILABLE_SHARES, vs);
        /*
        if (MsgGetUserSettingsDN(client->dn, configDn, vs, FALSE)) {
            MDBRead(configDn, MSGSRV_A_AVAILABLE_SHARES, vs);
        }
        */

        for (used = 0; used < vs->Used; used++) {
            if (strncmp(vs->Value[used], pattern, 2) != 0) {
                continue;
            }

            displayName = strrchr(vs->Value[used], '\r');
            if (displayName && (XplStrCaseCmp(++displayName, name) == 0)) {
                ptr = strchr(vs->Value[used], '\r');

                *(--displayName) = '\0';
                *owner = MemStrdup(ptr + 1);

                *ptr = '\0';
                *trueName = MemStrdup(vs->Value[used] + 2);

                result = TRUE;
                break;
            }
        }
    } else {
        LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(MDBValueStruct), __LINE__, NULL, 0);
        MemFree(pattern);
        return(FALSE);
    }

    if (!result) {
        memcpy(pattern, type, 2);
        pattern[2] = '\0';

        MDBFreeValues(vs);
        MDBRead(MSGSRV_ROOT, MSGSRV_A_AVAILABLE_SHARES, vs);
        for (used = 0; used < vs->Used; used++) {
            if (strncmp(vs->Value[used], pattern, 2) != 0) {
                continue;
            }

            displayName = strrchr(vs->Value[used], '\r');
            if (displayName && (XplStrCaseCmp(++displayName, name) == 0)) {
                ptr = strchr(vs->Value[used], '\r');

                *(--displayName) = '\0';
                *owner = MemStrdup(ptr + 1);

                *ptr = '\0';
                *trueName = MemStrdup(vs->Value[used] + 2);

                result = TRUE;
                break;
            }
        }
    }

    if (result) {
        MDBFreeValues(vs);

        *permissions = 0;

        result = FALSE;
        if (MsgFindObject(*owner, dn, NULL, NULL, NULL)) {
            length = sprintf(pattern, "MB%s\r", *trueName);

            count = strlen(client->user);
            memcpy(pattern + length, client->user, count);
            count += length;

            //FIXME
            //MsgGetUserSetting(dn, MSGSRV_A_OWNED_SHARES, vs);
            /*
            if (MsgGetUserSettingsDN(dn, configDn, vs, FALSE)) {
                MDBRead(configDn, MSGSRV_A_OWNED_SHARES, vs);
            }
            */

            for (used = 0; used < vs->Used; used++) {
                if ((XplStrNCaseCmp(vs->Value[used], pattern, count) != 0) 
                        && ((XplStrNCaseCmp(vs->Value[used], pattern, length) != 0) 
                                || (vs->Value[used][length] != '-') 
                                || (vs->Value[used][length + 1] != '\r'))) {
                    continue;
                }

                ptr = strrchr(vs->Value[used], '\r');
                if (ptr) {
                    *permissions = atol(++ptr);
                }

                result = TRUE;
                break;
            }
        }
    }

    MDBDestroyValueStruct(vs);

    return(result);
}

#if CAL_IMPLEMENTED
static BOOL 
ProcessAcceptDeclineAction(RulesClient *client, BongoRuleCondition *condition, BongoRuleAction *action, BOOL *copy, int *flags)
{
    int ccode;
    int count;
    int length;
    unsigned long minute;
    unsigned long hour;
    unsigned long day;
    unsigned long month;
    unsigned long year;
    unsigned char *ptr;
    unsigned char *subject = NULL;
    unsigned char *email = NULL;
    BOOL accepted = (action->type == RULE_ACT_ACCEPT)? TRUE: FALSE;
    BOOL result = FALSE;
    BOOL write;
    ICalVAttendee *attendee = NULL;
    MDBValueStruct *vs;

    if (!client->iCal.object && !(client->flags & RULES_CLIENT_FLAG_ICAL_CHECKED)) {
        ccode = -1;
        GET_MIME_STRUCTURE(client, ccode);

        if (ccode != -1) {
            client->iCal.object = GetCalendarData(client);
            client->flags |= RULES_CLIENT_FLAG_ICAL_CHECKED;
        }
    }

    vs = NULL;
    
    if (client->iCal.object 
            && (client->iCal.object->Method == ICAL_METHOD_REQUEST) 
            && client->iCal.object->Organizer 
            && ((vs = MDBCreateValueStruct(Rules.handle.directory, NULL)) != NULL) 
            && ((email = MsgGetUserEmailAddress(client->dn, vs, NULL, 0)) != NULL)) {
        MDBDestroyValueStruct(vs);

        attendee = client->iCal.object->Attendee;
        while (attendee) {
            if (XplStrCaseCmp(attendee->Address, email) != 0) {
                attendee = attendee->Next;
                continue;
            }

            break;
        }

        if (!attendee) {
            attendee = client->iCal.object->Attendee;
            while (attendee) {
                if (XplStrCaseCmp(attendee->Address, client->user) != 0) {
                    attendee = attendee->Next;
                    continue;
                }

                break;
            }
        }

        if (attendee 
                && ((subject = (unsigned char *)MemMalloc(16)) != NULL) 
                && ((ccode = NMAPSendCommandF(client->conn, "QGREP %s Subject:\r\n", client->queueID)) != -1)) {
            memcpy(subject, "Subject: Re: ", 13);

            write = FALSE;
            count = 13;

            while ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 2002) {
                if (count == 13) {
                    length = strlen(client->line);
                    ptr = MemRealloc(subject, count + length + 1);
                    if (ptr) {
                        subject = ptr;
                        write = TRUE;

                        if (client->line[8] == ' ') {
                            count = length - 9;
                            memcpy(subject + 13, client->line + 9, count);

                            count += 13;
                        } else if (client->line[8] == '\0') {
                            count = 13;
                        } else {
                            count = length - 8;
                            memcpy(subject + 13, client->line + 8, count);

                            count += 13;
                        }

                        subject[count] = '\0';

                        continue;
                    }

                    LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, count + length + 1, __LINE__, NULL, 0);
                    continue;
                }

                if (write) {
                    length = strlen(client->line);
                    ptr = MemRealloc(subject, count + length + 3);
                    if (ptr) {
                        subject = ptr;

                        subject[count++] = '\r';
                        subject[count++] = '\n';

                        memcpy(subject + count, client->line, length);

                        count += length;

                        subject[count] = '\0';
                        continue;
                    }

                    write = FALSE;

                    LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, count + length + 3, __LINE__, NULL, 0);
                    continue;
                }
            }

            if ((ccode == NMAP_OK) 
                    && ((ccode = NMAPSendCommand(client->conn, "QCREA\r\n", 7)) != -1) 
                    && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                    && ((ccode = NMAPSendCommandF(client->conn, "QSTOR FROM %s %s\r\n", email, email)) != -1) 
                    && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                    && ((ccode = NMAPSendCommandF(client->conn, "QSTOR TO %s %s %d\r\n", client->iCal.object->Organizer->Address, client->iCal.object->Organizer->Address, 0)) != -1) 
                    && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                    && ((ccode = ConnWrite(client->conn, "QSTOR MESSAGE\r\n", 15)) != -1) 
                    && ((ccode = ConnWriteF(client->conn, "From: %s\r\n", email)) != -1) 
                    && ((ccode = ConnWriteF(client->conn, "To: %s\r\n", client->iCal.object->Organizer->Address)) != -1) 
                    && (MsgGetRFC822Date(-1, 0, client->line)) 
                    && ((ccode = ConnWriteF(client->conn, "Date: %s\r\n", client->line)) != -1) 
                    && ((ccode = ConnWrite(client->conn, subject, count)) != -1) 
                    && ((ccode = ConnWrite(client->conn, accepted? " (Accepted)\r\n": " (Declined)\r\n", 13)) != -1) 
                    && ((ccode = ConnWrite(client->conn, "X-Sender: Bongo Project Rules Server\r\n", 37)) != -1) 
                    && ((ccode = ConnWrite(client->conn, "MIME-Version: 1.0\r\n", 19)) != -1) 
                    && ((ccode = ConnWrite(client->conn, "Content-Type: text/calendar; method=REPLY; charset=\"UTF-8\"\r\n\r\n", 62)) != -1) 
                    && ((ccode = ConnWrite(client->conn, "BEGIN:VCALENDAR\r\nPRODID:-//Novell Inc//NetMail RuleSrv//\r\nVERSION:2.0\r\nMETHOD:REPLY\r\n", 85)) != -1)) {
                switch(client->iCal.object->Type) {
                    case ICAL_VEVENT: {
                        ccode = ConnWrite(client->conn, "BEGIN:VEVENT\r\n", 14);
                        break;
                    }

                    case ICAL_VTODO: {
                        ccode = ConnWrite(client->conn, "BEGIN:VTODO\r\n", 13);
                        break;
                    }

                    case ICAL_VJOURNAL: {
                        ccode = ConnWrite(client->conn, "BEGIN:VJOURNAL\r\n", 16);
                        break;
                    }
                }

                if ((ccode != -1) 
                        && ((ccode = ConnWriteF(client->conn, "ATTENDEE;CN=%s;PARTSTAT=%s:MAILTO:%s\r\n", attendee->Name, accepted? "ACCEPTED": "DECLINED", email)) != -1) 
                        && ((ccode = ConnWriteF(client->conn, "SEQUENCE:%lu\r\n", client->iCal.object->Sequence)) != -1) 
                        && ((ccode = ConnWriteF(client->conn, "UID:%s\r\n", client->iCal.object->UID)) != -1) 
                        && (MsgGetDMY(time(NULL), &day, &month, &year, &hour, &minute, NULL)) 
                        && ((ccode = ConnWriteF(client->conn, "DTSTAMP:%04lu%02lu%02luT%02lu%02lu00Z\r\n", year, month, day, hour, minute)) != -1)) {
                    switch(client->iCal.object->Type) {
                        case ICAL_VEVENT: {
                            ccode = ConnWrite(client->conn, "END:VEVENT\r\nEND:VCALENDAR\r\n", 27);
                            break;
                        }

                        case ICAL_VTODO: {
                            ccode = ConnWrite(client->conn, "END:VTODO\r\nEND:VCALENDAR\r\n", 26);
                            break;
                        }

                        case ICAL_VJOURNAL: {
                            ccode = ConnWrite(client->conn, "END:VJOURNAL\r\nEND:VCALENDAR\r\n", 29);
                            break;
                        }
                    }
                }
            } else {
                ccode = -1;
            }

            if (ccode != -1) {
                if (((ccode = NMAPSendCommand(client->conn, "\r\n.\r\nQRUN\r\n", 11)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
                    if ((accepted) 
                            && ((client->store = ConnectToUserNMAPStore(client)) != NULL)) {
                        if (((ccode = NMAPSendCommand(client->store, "CSOPEN MAIN\r\n", 13)) != -1) 
                                && ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                                && ((ccode = NMAPSendCommand(client->store, "CSSTOR MAIN\r\n", 13)) != -1) 
                                && ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                                && ((ccode = ConnWrite(client->store, client->iCal.data, client->iCal.length)) != -1) 
                                && ((ccode = NMAPSendCommand(client->store, "\r\n.\r\n", 5)) != -1) 
                                && ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                                && ((ccode = NMAPSendCommand(client->store, "CSUPDA\r\n", 13)) != -1)) {
                            do {
                                ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE);
                            } while (ccode == 6001);

                            if (((ccode = NMAPSendCommandF(client->store, "CSFIND %lu %s\r\n", client->iCal.object->Sequence, client->iCal.object->UID)) != -1) 
                                    && ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                                    && ((ccode = NMAPSendCommandF(client->store, "CSATND %lu A %s\r\n", atol(client->line), email)) != -1)) {
                                /* fixme
                                    The NMAP CSATND command returns NMAP_OK on success and if the attendee was not found!
                                    We need to return an error on not found so we can try using 'client->user' instead of
                                    'email' as the attendee.
                                */

                                ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE);
                            }
                        }

                        NMAPQuit(client->store);
                        ConnFree(client->store);
                        client->store = NULL;
                    }
                }
            } else {
                NMAPSendCommand(client->conn, "\r\n.\r\nQABRT\r\n", 12);
                NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
                NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
            }

            MemFree(subject);
        } else if (attendee && !subject) {
            LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, 16, __LINE__, NULL, 0);
        } else if (subject) {
            MemFree(subject);
            subject = NULL;
        }

        MemFree(email);
    } else {
        if (vs) {
            MDBDestroyValueStruct(vs);
        }
    }

    return(result);
}
#endif

static unsigned char *
ProcessSender(unsigned char *line)
{
    register unsigned char *ptr = line;
    register unsigned char c;
    register unsigned char *from;

    while (*ptr && !isspace(*ptr)) {
        ptr++;
    }

    c = *ptr;
    *ptr = '\0';
    from = MemStrdup(line);
    *ptr = c;

    return(from);
}

static int 
ProcessRules(RulesClient *client, BOOL *copy, int *flags)
{
    int ccode = 0;
    unsigned long used;
    unsigned long start;
    unsigned long size;
    unsigned long header;
    unsigned char *ptr;
    unsigned char *owner;
    BOOL result;
    BongoRuleAction *action;
    BongoRuleCondition *condition;
    BongoRule *rule;

    rule = client->rules.head;
    while (rule && (ccode != -1)) {
        condition = rule->conditions.head;
        result = FALSE;
        while (condition && (ccode != -1)) {
            result = FALSE;
            switch (condition->type) {
                case RULE_COND_ANY: {
                    result = TRUE;
                    break;
                }

                case RULE_COND_FROM: 
                case RULE_COND_FROM_NOT: {
                    if ((condition->string[0][0] != '\"') || (condition->string[0][condition->length[0]] != '\"')) {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s From: \"%s\"\r\n", client->queueID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s From: %s\r\n", client->queueID, condition->string[0]);
                    }

                    if ((ccode != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
                            && (((condition->type == RULE_COND_FROM) && (ccode == NMAP_OK)) || ((condition->type == RULE_COND_FROM_NOT) && (ccode == 4262)))) {
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_TO: 
                case RULE_COND_TO_NOT: {
                    if ((condition->string[0][0] != '\"') || (condition->string[0][condition->length[0]] != '\"')) {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s To: \"%s\"\r\n", client->queueID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s To: %s\r\n", client->queueID, condition->string[0]);
                    }

                    if ((ccode != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
                            && (((condition->type == RULE_COND_TO) && (ccode == NMAP_OK)) || ((condition->type == RULE_COND_TO_NOT) && (ccode == 4262)))) {
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_SUBJECT: 
                case RULE_COND_SUBJECT_NOT: {
                    if ((condition->string[0][0] != '\"') || (condition->string[0][condition->length[0]] != '\"')) {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s Subject: \"%s\"\r\n", client->queueID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s Subject: %s\r\n", client->queueID, condition->string[0]);
                    }

                    if ((ccode != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
                            && (((condition->type == RULE_COND_SUBJECT) && (ccode == NMAP_OK)) || ((condition->type == RULE_COND_SUBJECT_NOT) && (ccode == 4262)))) {
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_PRIORITY: 
                case RULE_COND_PRIORITY_NOT: {
                    if ((condition->string[0][0] != '\"') || (condition->string[0][condition->length[0]] != '\"')) {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s X-Priority: \"%s\"\r\n", client->queueID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s X-Priority: %s\r\n", client->queueID, condition->string[0]);
                    }

                    if ((ccode != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
                            && (((condition->type == RULE_COND_PRIORITY) && (ccode == NMAP_OK)) || ((condition->type == RULE_COND_PRIORITY_NOT) && (ccode == 4262)))) {
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_HEADER: 
                case RULE_COND_HEADER_NOT: {
                    if ((condition->string[1][0] != '\"') || (condition->string[1][condition->length[1]] != '\"')) {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s %s: \"%s\"\r\n", client->queueID, condition->string[0], condition->string[1]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s %s: %s\r\n", client->queueID, condition->string[0], condition->string[1]);
                    }

                    if ((ccode != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
                            && (((condition->type == RULE_COND_HEADER) && (ccode == NMAP_OK)) || ((condition->type == RULE_COND_HEADER_NOT) && (ccode == 4262)))) {
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_BODY: 
                case RULE_COND_BODY_NOT: {
                    GET_MIME_STRUCTURE(client, ccode);

                    for (used = 0; used < client->mime->Used; used++) {
                        ptr = client->mime->Value[used];

                        if ((XplStrNCaseCmp(ptr, "2002-text ", 10) == 0) 
                                || (XplStrNCaseCmp(ptr, "2002 text ", 10) == 0)) {
                            GET_MIME_SIZES(ptr, start, size, header);

                            if ((condition->string[0][0] != '\"') || (condition->string[0][condition->length[0]] != '\"')) {
                                ccode = NMAPSendCommandF(client->conn, "QSRCH BRAW %s %lu %lu \"%s\"\r\n", client->queueID, start + header, start + size, condition->string[0]);
                            } else {
                                ccode = NMAPSendCommandF(client->conn, "QSRCH BRAW %s %lu %lu %s\r\n", client->queueID, start + header, start + size, condition->string[0]);
                            }

                            if ((ccode != -1) 
                                    && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
                                    && (((condition->type == RULE_COND_BODY) && (ccode == NMAP_OK)) || ((condition->type == RULE_COND_BODY_NOT) && (ccode == 4262)))) {
                                result = TRUE;

                                break;
                            }
                        }
                    }

                    break;
                }

                case RULE_COND_SIZE_MORE: {
                    if (((ccode = NMAPSendCommandF(client->conn, "QINFO %s\r\n", client->queueID)) != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 2001) 
                            && (sscanf(client->line, "%*s %lu", (long unsigned int *)&size) == 1) 
                            && (size > condition->count[0])) { 
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_SIZE_LESS: {
                    if (((ccode = NMAPSendCommandF(client->conn, "QINFO %s\r\n", client->queueID)) != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 2001) 
                            && (sscanf(client->line, "%*s %lu", (long unsigned int *)&size) == 1) 
                            && (size < condition->count[0])) { 
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_TIMEOFDAYLESS: 
                case RULE_COND_TIMEOFDAYMORE: 
                case RULE_COND_WEEKDAYIS: 
                case RULE_COND_MONTHDAYIS: 
                case RULE_COND_MONTHDAYISNOT: 
                case RULE_COND_MONTHDAYLESS: 
                case RULE_COND_MONTHDAYMORE: 
                case RULE_COND_MONTHIS: 
                case RULE_COND_MONTHISNOT: {
#if CAL_IMPLEMENTED
                    if (!client->iCal.object && !(client->flags & RULES_CLIENT_FLAG_ICAL_CHECKED)) {
                        GET_MIME_STRUCTURE(client, ccode);

                        if (ccode != -1) {
                            client->iCal.object = GetCalendarData(client);
                            client->flags |= RULES_CLIENT_FLAG_ICAL_CHECKED;
                        }
                    }

                    if (client->iCal.object) {
                        break;
                    } else {
                        break;
                    }

                    break;
#endif
                }

                case RULE_COND_FREE: {
#if CAL_IMPLEMENTED
                    if (!(client->flags & RULES_CLIENT_FLAG_FREE_CHECKED)) {
                        client->flags |= RULES_CLIENT_FLAG_FREE_CHECKED;

                        if (!client->iCal.object && !(client->flags & RULES_CLIENT_FLAG_ICAL_CHECKED)) {
                            GET_MIME_STRUCTURE(client, ccode);

                            if (ccode != -1) {
                                client->iCal.object = GetCalendarData(client);
                                client->flags |= RULES_CLIENT_FLAG_ICAL_CHECKED;
                            }
                        }

                        if (client->iCal.object 
                                && (client->iCal.object->Method == ICAL_METHOD_REQUEST) 
                                && ((client->store = ConnectToUserNMAPStore(client)) != NULL)) {
                            if (((ccode = NMAPSendCommand(client->store, "CSOPEN MAIN\r\n", 13)) != -1) 
                                    && ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                                    && ((ccode = NMAPSendCommandF(client->store, "CSFILT %lu %lu\r\n", client->iCal.object->Start.UTC + 60, client->iCal.object->End.UTC - 60)) != -1)) {
                                result = TRUE;

                                while ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == 2002) {
                                    if ((sscanf(client->line, "%*u %lu", &header) == 1) && (header == 1)) {
                                        result = FALSE;
                                    }
                                }
                            }

                            NMAPQuit(client->store);
                            ConnFree(client->store);
                            client->store = NULL;
                        } else {
                            break;
                        }
                    }
#else
                    result = TRUE;
#endif                    
                    break;
                }

                case RULE_COND_FREENOT: {
#if CAL_IMPLEMENTED
                    if (!(client->flags & RULES_CLIENT_FLAG_FREE_CHECKED)) {
                        client->flags |= RULES_CLIENT_FLAG_FREE_CHECKED;

                        if (!client->iCal.object && !(client->flags & RULES_CLIENT_FLAG_ICAL_CHECKED)) {
                            GET_MIME_STRUCTURE(client, ccode);

                            if (ccode != -1) {
                                client->iCal.object = GetCalendarData(client);
                                client->flags |= RULES_CLIENT_FLAG_ICAL_CHECKED;
                            }
                        }

                        if (client->iCal.object 
                                && (client->iCal.object->Method == ICAL_METHOD_REQUEST) 
                                && ((client->store = ConnectToUserNMAPStore(client)) != NULL)) {
                            if (((ccode = NMAPSendCommand(client->store, "CSOPEN MAIN\r\n", 13)) != -1) 
                                    && ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                                    && ((ccode = NMAPSendCommandF(client->store, "CSFILT %lu %lu\r\n", client->iCal.object->Start.UTC + 60, client->iCal.object->End.UTC - 60)) != -1)) {
                                while ((ccode = NMAPReadAnswer(client->store, client->line, CONN_BUFSIZE, TRUE)) == 2002) {
                                    if ((sscanf(client->line, "%*u %lu", &header) == 1) && (header == 1)) {
                                        result = TRUE;
                                    }
                                }
                            }

                            NMAPQuit(client->store);
                            ConnFree(client->store);
                            client->store = NULL;
                        } else {
                            break;
                        }
                    }
#else
                    result = FALSE;
#endif
                    break;
                }


                case RULE_COND_HASMIMETYPE: {
                    ptr = strchr(condition->string[0], '/');
                    if (ptr) {
                        *ptr = ' ';
                    }

                    GET_MIME_STRUCTURE(client, ccode);

                    for (used = 0; used < client->mime->Used; used++) {
                        ptr = client->mime->Value[used];

                        if (XplStrNCaseCmp(ptr + 5, condition->string[0], condition->length[0]) == 0) {
                            result = TRUE;
                        }
                    }

                    break;
                }

                case RULE_COND_HASMIMETYPENOT: {
                    ptr = strchr(condition->string[0], '/');
                    if (ptr) {
                        *ptr = ' ';
                    }

                    GET_MIME_STRUCTURE(client, ccode);

                    result = TRUE;
                    for (used = 0; used < client->mime->Used; used++) {
                        ptr = client->mime->Value[used];

                        if (XplStrNCaseCmp(ptr + 5, condition->string[0], condition->length[0]) == 0) {
                            result = FALSE;
                        }
                    }

                    break;
                }

                default: {
                    break;
                }
            }

            if (result) {
                while (condition->next && (condition->operand == RULE_ACT_BOOL_OR)) {
                    condition = condition->next;
                }

                if (!condition->next) {
                    break;
                }

                condition = condition->next;
                continue;
            }

            if (condition->next && (condition->operand == RULE_ACT_BOOL_OR)) {
                condition = condition->next;
                continue;
            }

            break;
        }

        if (result) {
            action = rule->actions.head;
            while (action && (ccode != -1)) {
                switch (action->type) {
                    case RULE_ACT_STOP: {
                        return(ccode);
                    }

                    case RULE_ACT_DELETE: {
                        LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_RULE_DELETE, LOG_NOTICE, 0, client->user, NULL, 0, 0, NULL, 0);

                        *copy = FALSE;
                        break;
                    }

                    case RULE_ACT_FORWARD: {
                        LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_RULE_FORWARD, LOG_NOTICE, 0, client->user, action->string[0], 0, 0, NULL, 0);

                        *flags &= ~(DSN_SUCCESS | DSN_TIMEOUT | DSN_FAILURE | DSN_HEADER | DSN_BODY);

                        ccode = NMAPSendCommandF(client->conn, "QMOD TO %s %s %lu\r\n", action->string[0], client->recipient, (unsigned long)(*flags | NO_RULEPROCESSING));
                        break;
                    }

                    case RULE_ACT_COPY: {
                        LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_RULE_COPY, LOG_NOTICE, 0, action->string[0], NULL, 0, 0, NULL, 0);

                        *flags &= ~(DSN_SUCCESS | DSN_TIMEOUT | DSN_FAILURE | DSN_HEADER | DSN_BODY);

                        ccode = NMAPSendCommandF(client->conn, "QMOD TO %s %s %lu\r\n", action->string[0], client->recipient, (unsigned long)(*flags | NO_RULEPROCESSING));
                        break;
                    }

                    case RULE_ACT_MOVE: {
                        *copy = FALSE;

                        owner = NULL;
                        ptr = NULL;
                        used = 0;

                        ptr = action->string[0];
                        while ((ptr = strchr(ptr, ' ')) != NULL) {
                            *ptr++ = 0x7F;
                        }

                        if (!IsNMAPShare(client, action->string[0], "MB", &owner, &ptr, &used)) {
                            LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_RULE_MOVE, LOG_NOTICE, 0, client->user, action->string[0], 0, 0, NULL, 0);

                            ccode = NMAPSendCommandF(client->conn, "QMOD MAILBOX %s %s %lu %s\r\n", client->user, client->recipient, (unsigned long)(*flags | NO_RULEPROCESSING), action->string[0]);
                        } else if (owner && ptr && (used & NMAP_SHARE_INSERT)) {
                            LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_RULE_MOVE, LOG_NOTICE, 0, client->recipient, ptr, 0, 0, NULL, 0);

                            ccode = NMAPSendCommandF(client->conn, "QMOD MAILBOX %s %s %lu %s\r\n", owner, client->recipient, (unsigned long)(*flags | NO_RULEPROCESSING), ptr);
                        } else {
                            LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_RULE_MOVE, LOG_NOTICE, 0, client->user, "INBOX", 0, 0, NULL, 0);

                            ccode = NMAPSendCommandF(client->conn, "QMOD MAILBOX %s %s %lu INBOX\r\n", client->user, client->recipient, (unsigned long)(*flags | NO_RULEPROCESSING));
                        }

                        if (owner) {
                            MemFree(owner);
                            owner = NULL;
                        }

                        if (ptr) {
                            MemFree(ptr);
                            ptr = NULL;
                        }

                        break;
                    }

                    case RULE_ACT_ACCEPT: 
                    case RULE_ACT_DECLINE: {
#if CAL_IMPLEMENTED
                        ccode = ProcessAcceptDeclineAction(client, condition, action, copy, flags);
#else
                        ccode = 0;
#endif
                        
                        break;
                    }

                    case RULE_ACT_REPLY: 
                    case RULE_ACT_BOUNCE: 
                    default: {
                        break;
                    }
                }

                if ((ccode != -1) && ((action = action->next) != NULL)) {
                    continue;
                }

                break;
            }
        }

        rule = rule->next;
    }

    return(ccode);
}

static int 
ProcessConnection(RulesClient *client)
{
    int ccode;
    int length;
    int flags;
    unsigned char *ptr;
    unsigned char *cur;
    unsigned char *line;
    unsigned char *from = NULL;
    BOOL copy;
    MDBValueStruct *vs;

    if (((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
            && (ccode == 6020) 
            && ((ptr = strchr(client->line, ' ')) != NULL)) {
        *ptr++ = '\0';

        strcpy(client->queueID, client->line);

        length = atoi(ptr);
        client->envelope = MemMalloc(length + 3);
    } else {
        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    if (client->envelope) {
        sprintf(client->line, "Rules: %s", client->queueID);
        XplRenameThread(XplGetThreadID(), client->line);

        ccode = NMAPReadCount(client->conn, client->envelope, length);
    } else {
        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 6021)) {
        client->envelope[length] = '\0';

        cur = client->envelope;
    } else {
        MemFree(client->envelope);
        client->envelope = NULL;

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    while (*cur) {
        copy = TRUE;
        line = strchr(cur, 0x0A);
        if (line) {
            if (line[-1] == 0x0D) {
                line[-1] = '\0';
            } else {
                *line = '\0';
            }

            line++;
        } else {
            line = cur + strlen(cur);
        }

        switch (cur[0]) {
            case QUEUE_FROM: {
                if (!from) {
                    from = ProcessSender(cur + 1);
                    if (from) {
                        copy = FALSE;

                        ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                    }
                }

                break;
            }

            case QUEUE_CALENDAR_LOCAL: 
            case QUEUE_RECIP_LOCAL: {
                ptr = strrchr(cur, ' ');
                if (ptr) {
                    flags = atol(ptr + 1);
                } else {
                    flags = 0;
                }

                if (!(flags & NO_RULEPROCESSING)) {
                    ptr = strchr(cur, ' ');
                    if (ptr) {
                        *ptr = '\0';
                    }

                    strcpy(client->recipient, cur + 1);

                    if (ptr) {
                        *ptr = ' ';
                    }

                    vs = MDBCreateValueStruct(Rules.handle.directory, NULL);
                    if (vs) {
                        if (MsgFindObject(client->recipient, client->dn, NULL, &(client->nmap), vs)) {
                            strcpy(client->user, vs->Value[0]);

                            if (MDBFreeValues(vs)) {
                                // FIXME
                                //    && MsgGetUserFeature(client->dn, FEATURE_RULES, MSGSRV_A_RULE, vs)) {
                                ParseRules(client, vs);
                                if (client->rules.head) {
                                    ccode = ProcessRules(client, &copy, &flags);
                                }
                            }
                        } else {
                            LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_UNKNOWN_USER, LOG_NOTICE, 0, client->recipient, "", XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                        }

                        MDBDestroyValueStruct(vs);
                    } else {
                        LoggerEvent(Rules.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(MDBValueStruct), __LINE__, NULL, 0);
                    }
                }

                break;
            }

            case QUEUE_ADDRESS: 
            case QUEUE_BOUNCE: 
            case QUEUE_RECIP_MBOX_LOCAL: 
            case QUEUE_DATE: 
            case QUEUE_ID: 
            case QUEUE_RECIP_REMOTE: 
            case QUEUE_THIRD_PARTY: 
            case QUEUE_FLAGS: 
            default: {
                break;
            }
        }

        if (copy && (ccode != -1)) {
            ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
        }

        cur = line;
    }

    if ((ccode != -1) 
            && ((ccode = NMAPSendCommand(client->conn, "QDONE\r\n", 7)) != -1)) {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
    }

    if (client->envelope) {
        MemFree(client->envelope);
        client->envelope = NULL;
    }

    return(0);
}

static void 
HandleConnection(void *param)
{
    int ccode;
    long threadNumber = (long)param;
    time_t sleep = time(NULL);
    time_t wokeup;
    RulesClient *client;

    if ((client = RulesClientAlloc()) == NULL) {
        XplConsolePrintf("bongorules: New worker failed to startup; out of memory.\r\n");

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);

        XplSafeDecrement(Rules.nmap.worker.active);

        return;
    }

    do {
        XplRenameThread(XplGetThreadID(), "Rules Worker");

        XplSafeIncrement(Rules.nmap.worker.idle);

        XplWaitOnLocalSemaphore(Rules.nmap.worker.todo);

        XplSafeDecrement(Rules.nmap.worker.idle);

        wokeup = time(NULL);

        XplWaitOnLocalSemaphore(Rules.nmap.semaphore);

        client->conn = Rules.nmap.worker.tail;
        if (client->conn) {
            Rules.nmap.worker.tail = client->conn->queue.previous;
            if (Rules.nmap.worker.tail) {
                Rules.nmap.worker.tail->queue.next = NULL;
            } else {
                Rules.nmap.worker.head = NULL;
            }
        }

        XplSignalLocalSemaphore(Rules.nmap.semaphore);

        if (client->conn) {
            if (ConnNegotiate(client->conn, Rules.nmap.ssl.context)) {
                ccode = ProcessConnection(client);
            } else {
                NMAPSendCommand(client->conn, "QDONE\r\n", 7);
                NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
            }
        }

        if (client->conn) {
            ConnFlush(client->conn);
        }

        FreeClientData(client);

        /* Live or die? */
        if (threadNumber == XplSafeRead(Rules.nmap.worker.active)) {
            if ((wokeup - sleep) > Rules.nmap.sleepTime) {
                break;
            }
        }

        sleep = time(NULL);

        RulesClientAllocCB(client, NULL);
    } while (Rules.state == RULES_STATE_RUNNING);

    FreeClientData(client);

    RulesClientFree(client);

    XplSafeDecrement(Rules.nmap.worker.active);

    XplExitThread(TSR_THREAD, 0);

    return;
}

static void 
RulesServer(void *ignored)
{
    int i;
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSafeIncrement(Rules.server.active);

    XplRenameThread(XplGetThreadID(), "Anti-Spam Server");

    while (Rules.state < RULES_STATE_STOPPING) {
        if (ConnAccept(Rules.nmap.conn, &conn) != -1) {
            if (Rules.state < RULES_STATE_STOPPING) {
                conn->ssl.enable = FALSE;

                QUEUE_WORK_TO_DO(conn, id, ccode);
                if (!ccode) {
                    XplSignalLocalSemaphore(Rules.nmap.worker.todo);

                    continue;
                }
            }

            ConnWrite(conn, "QDONE\r\n", 7);
            ConnClose(conn, 0);

            ConnFree(conn);
            conn = NULL;

            continue;
        }

        switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO: 
#endif
            case EINTR: {
                if (Rules.state < RULES_STATE_STOPPING) {
                    LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }

            default: {
                if (Rules.state < RULES_STATE_STOPPING) {
                    XplConsolePrintf("bongorules: Exiting after an accept() failure; error %d\r\n", errno);

                    LoggerEvent(Rules.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                    Rules.state = RULES_STATE_STOPPING;
                }

                break;
            }
        }

        break;
    }

    /* Shutting down */
    Rules.state = RULES_STATE_UNLOADING;

#if VERBOSE
    XplConsolePrintf("bongorules: Shutting down.\r\n");
#endif

    id = XplSetThreadGroupID(Rules.id.group);

    if (Rules.nmap.conn) {
        ConnClose(Rules.nmap.conn, 1);
        Rules.nmap.conn = NULL;
    }

    if (Rules.nmap.ssl.enable) {
        Rules.nmap.ssl.enable = FALSE;

        if (Rules.nmap.ssl.conn) {
            ConnClose(Rules.nmap.ssl.conn, 1);
            Rules.nmap.ssl.conn = NULL;
        }

        if (Rules.nmap.ssl.context) {
            ConnSSLContextFree(Rules.nmap.ssl.context);
            Rules.nmap.ssl.context = NULL;
        }
    }

    ConnCloseAll(1);

    for (i = 0; (XplSafeRead(Rules.server.active) > 1) && (i < 60); i++) {
        XplDelay(1000);
    }

#if VERBOSE
    XplConsolePrintf("bongorules: Shutting down %d queue threads\r\n", XplSafeRead(Rules.nmap.worker.active));
#endif

    XplWaitOnLocalSemaphore(Rules.nmap.semaphore);

    ccode = XplSafeRead(Rules.nmap.worker.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(Rules.nmap.worker.todo);
    }

    XplSignalLocalSemaphore(Rules.nmap.semaphore);

    for (i = 0; XplSafeRead(Rules.nmap.worker.active) && (i < 60); i++) {
        XplDelay(1000);
    }

    if (XplSafeRead(Rules.server.active) > 1) {
        XplConsolePrintf("bongorules: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(Rules.server.active) - 1);
    }

    if (XplSafeRead(Rules.nmap.worker.active)) {
        XplConsolePrintf("bongorules: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(Rules.nmap.worker.active));
    }

    LoggerClose(Rules.handle.logging);
    Rules.handle.logging = NULL;

    XplCloseLocalSemaphore(Rules.nmap.worker.todo);
    XplCloseLocalSemaphore(Rules.nmap.semaphore);

    StreamioShutdown();
    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    MemPrivatePoolFree(Rules.nmap.pool);

    MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);
#if VERBOSE
    XplConsolePrintf("bongorules: Shutdown complete\r\n");
#endif

    XplSignalLocalSemaphore(Rules.sem.main);
    XplWaitOnLocalSemaphore(Rules.sem.shutdown);

    XplCloseLocalSemaphore(Rules.sem.shutdown);
    XplCloseLocalSemaphore(Rules.sem.main);

    XplSetThreadGroupID(id);

    return;
}

static BOOL 
ReadConfiguration(void)
{
    unsigned long used;

    Rules.flags = RULES_FLAG_USER_RULES | RULES_FLAG_SYSTEM_RULES;
    Rules.officialName[0] = '\0';

    return(TRUE);
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = TRUE;
        Rules.state = RULES_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(Rules.sem.shutdown);

        id = XplSetThreadGroupID(Rules.id.group);
        ConnClose(Rules.nmap.conn, 1);
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(Rules.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (Rules.state < RULES_STATE_UNLOADING) {
                Rules.state = RULES_STATE_UNLOADING;
            }

            break;
        }

        case SIGINT:
        case SIGTERM: {
            if (Rules.state == RULES_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (Rules.state < RULES_STATE_STOPPING) {
                Rules.state = RULES_STATE_STOPPING;
            }

            break;
        }

        default: {
            break;
        }
    }

    return;
}

static int 
QueueSocketInit(void)
{
    Rules.nmap.conn = ConnAlloc(FALSE);
    if (Rules.nmap.conn) {
        memset(&(Rules.nmap.conn->socketAddress), 0, sizeof(Rules.nmap.conn->socketAddress));

        Rules.nmap.conn->socketAddress.sin_family = AF_INET;
        Rules.nmap.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

        /* Get root privs back for the bind.  It's ok if this fails -
           the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);

        Rules.nmap.conn->socket = ConnServerSocket(Rules.nmap.conn, 2048);
        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongorules: Could not drop to unprivileged user '%s'\r\n", MsgGetUnprivilegedUser());
            ConnFree(Rules.nmap.conn);
            Rules.nmap.conn = NULL;
            return(-1);
        }

        if (Rules.nmap.conn->socket == -1) {
            XplConsolePrintf("bongorules: Could not bind to dynamic port\r\n");
            ConnFree(Rules.nmap.conn);
            Rules.nmap.conn = NULL;
            return(-1);
        }

        if (QueueRegister(MSGSRV_AGENT_ANTISPAM, Rules.nmap.queue, Rules.nmap.conn->socketAddress.sin_port) != REGISTRATION_COMPLETED) {
            XplConsolePrintf("bongorules: Could not register with bongonmap\r\n");
            ConnFree(Rules.nmap.conn);
            Rules.nmap.conn = NULL;
            return(-1);
        }
    } else {
        XplConsolePrintf("bongorules: Could not allocate connection.\r\n");
        return(-1);
    }

    return(0);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int                ccode;
    XplThreadID        id;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongorules: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
        return(1);
    }
    XplInit();

    XplSignalHandler(SignalHandler);

    Rules.id.main = XplGetThreadID();
    Rules.id.group = XplGetThreadGroupID();

    Rules.state = RULES_STATE_INITIALIZING;
    Rules.flags = 0;

    Rules.nmap.conn = NULL;
    Rules.nmap.queue = Q_RULE;
    Rules.nmap.pool = NULL;
    Rules.nmap.sleepTime = (5 * 60);
    Rules.nmap.ssl.conn = NULL;
    Rules.nmap.ssl.enable = FALSE;
    Rules.nmap.ssl.context = NULL;
    Rules.nmap.ssl.config.options = 0;

    Rules.handle.directory = NULL;
    Rules.handle.logging = NULL;

    strcpy(Rules.nmap.address, "127.0.0.1");

    XplSafeWrite(Rules.server.active, 0);

    XplSafeWrite(Rules.nmap.worker.idle, 0);
    XplSafeWrite(Rules.nmap.worker.active, 0);
    XplSafeWrite(Rules.nmap.worker.maximum, 100000);

    Rules.system.count = 0;
    Rules.system.rules = NULL;

    if (MemoryManagerOpen(MSGSRV_AGENT_ANTISPAM) == TRUE) {
        Rules.nmap.pool = MemPrivatePoolAlloc("Rules Connections", sizeof(RulesClient), 0, 3072, TRUE, FALSE, RulesClientAllocCB, NULL, NULL);
        if (Rules.nmap.pool != NULL) {
            XplOpenLocalSemaphore(Rules.sem.main, 0);
            XplOpenLocalSemaphore(Rules.sem.shutdown, 1);
            XplOpenLocalSemaphore(Rules.nmap.semaphore, 1);
            XplOpenLocalSemaphore(Rules.nmap.worker.todo, 1);
        } else {
            MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

            XplConsolePrintf("bongorules: Unable to create connection pool; shutting down.\r\n");
            return(-1);
        }
    } else {
        XplConsolePrintf("bongorules: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    ConnStartup(CONNECTION_TIMEOUT, TRUE);

    MDBInit();
    Rules.handle.directory = (MDBHandle)MsgInit();
    if (Rules.handle.directory == NULL) {
        XplBell();
        XplConsolePrintf("bongorules: Invalid directory credentials; exiting!\r\n");
        XplBell();

        MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

        return(-1);
    }

    StreamioInit();
    NMAPInitialize();

    SetCurrentNameSpace(NWOS2_NAME_SPACE);
    SetTargetNameSpace(NWOS2_NAME_SPACE);

    Rules.handle.logging = LoggerOpen("bongorules");
    if (!Rules.handle.logging) {
        XplConsolePrintf("bongorules: Unable to initialize logging; disabled.\r\n");
    }

    ReadConfiguration();

    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "rulesrv");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    if (QueueSocketInit() < 0) {
        XplConsolePrintf("bongorules: Exiting.\r\n");

        MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

        return -1;
    }

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongorules: Could not drop to unprivileged user '%s', exiting.\r\n", MsgGetUnprivilegedUser());

        MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

        return 1;
    }

    Rules.nmap.ssl.enable = FALSE;
    Rules.nmap.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
    Rules.nmap.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
    Rules.nmap.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

    Rules.nmap.ssl.context = ConnSSLContextAlloc(&(Rules.nmap.ssl.config));
    if (Rules.nmap.ssl.context) {
        Rules.nmap.ssl.enable = TRUE;
    }

    NMAPSetEncryption(Rules.nmap.ssl.context);

    Rules.state = RULES_STATE_RUNNING;

    XplStartMainThread(PRODUCT_SHORT_NAME, &id, RulesServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    return(0);
}
