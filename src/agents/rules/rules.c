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
// Parts Copyright (C) 2007-2008 Patrick Felt. See COPYING for details.

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

#include "rules.h"

RulesAgentGlobals RulesAgent = {{0,}, };

int
GetMimeStructure(RulesClient *c)
{
    int Result=0;
    char *dup;

    if (c->MimeStructure) {
        return Result;
    }
    c->MimeStructure = BongoArrayNew(sizeof(char *), 1);
    if ((Result = NMAPSendCommandF(c->conn, "QMIME %s\r\n", c->qID)) != -1) {
        while ((Result = NMAPReadAnswer(c->conn, c->line, CONN_BUFSIZE, FALSE)) == 2002) {
            dup = MemStrdup(c->line);
            BongoArrayAppendValue(c->MimeStructure, dup);
        }
    }
    return Result;
}

void
GetMimeSizes(unsigned char *p, unsigned long *s, unsigned long *l, unsigned long *h)
{
    unsigned char *eof = strrchr(p, '\"');
    if (eof) {
        eof++;
        sscanf(eof, " %lu %lu %lu", s, l, h);
    } else {
        sscanf(p, "%*u %*s %*s %*s %*s %*s %lu %lu %lu", s, l, h);
    }
}


void
RuleAppendCondition(BongoRule *rule, BongoRuleCondition *condition)
{
    if ((condition->prev = rule->conditions.tail) != NULL) {
        condition->prev->next = condition;
    } else {
        rule->conditions.head = condition;
    }
    rule->conditions.tail = condition;
}

void
RuleAppendAction(BongoRule *rule, BongoRuleAction *action)
{
    if ((action->prev = rule->actions.tail) != NULL) {
        action->prev->next = action;
    } else {
        rule->actions.head = action;
    }
    rule->actions.tail = action;
}

void
RuleAppend(RulesClient *client, BongoRule *rule)
{
    if ((rule->prev = client->RulesList.tail) != NULL) {
        rule->prev->next = rule;
    } else {
        client->RulesList.head = rule;
    }
    client->RulesList.tail = rule;
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

static int 
ProcessRules(RulesClient *client)
{
    int ccode = 0;
    unsigned long used;
    unsigned long start;
    unsigned long size;
    unsigned long header;
    unsigned char *ptr;
    unsigned char *owner;
    BOOL result, written=FALSE;
    BongoRuleAction *action;
    BongoRuleCondition *condition;
    BongoRule *rule;

    rule = client->RulesList.head;
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
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s From: \"%s\"\r\n", client->qID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s From: %s\r\n", client->qID, condition->string[0]);
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
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s To: \"%s\"\r\n", client->qID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s To: %s\r\n", client->qID, condition->string[0]);
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
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s Subject: \"%s\"\r\n", client->qID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s Subject: %s\r\n", client->qID, condition->string[0]);
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
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s X-Priority: \"%s\"\r\n", client->qID, condition->string[0]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s X-Priority: %s\r\n", client->qID, condition->string[0]);
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
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s %s: \"%s\"\r\n", client->qID, condition->string[0], condition->string[1]);
                    } else {
                        ccode = NMAPSendCommandF(client->conn, "QSRCH HEADER %s %s: %s\r\n", client->qID, condition->string[0], condition->string[1]);
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
                    GetMimeStructure(client);

                    for (used = 0; used < BongoArrayCount(client->MimeStructure); used++) {
                        ptr = BongoArrayIndex(client->MimeStructure, char *, used);

                        if ((XplStrNCaseCmp(ptr, "2002-text ", 10) == 0) 
                                || (XplStrNCaseCmp(ptr, "2002 text ", 10) == 0)) {
                            GetMimeSizes(ptr, &start, &size, &header);

                            if ((condition->string[0][0] != '\"') || (condition->string[0][condition->length[0]] != '\"')) {
                                ccode = NMAPSendCommandF(client->conn, "QSRCH BRAW %s %lu %lu \"%s\"\r\n", client->qID, start + header, start + size, condition->string[0]);
                            } else {
                                ccode = NMAPSendCommandF(client->conn, "QSRCH BRAW %s %lu %lu %s\r\n", client->qID, start + header, start + size, condition->string[0]);
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
                    if (((ccode = NMAPSendCommandF(client->conn, "QINFO %s\r\n", client->qID)) != -1) 
                            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 2001) 
                            && (sscanf(client->line, "%*s %lu", (long unsigned int *)&size) == 1) 
                            && (size > condition->count[0])) { 
                        result = TRUE;
                    }

                    break;
                }

                case RULE_COND_SIZE_LESS: {
                    if (((ccode = NMAPSendCommandF(client->conn, "QINFO %s\r\n", client->qID)) != -1) 
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
                }

                case RULE_COND_FREE: {
                    result = TRUE;
                    break;
                }

                case RULE_COND_FREENOT: {
                    result = FALSE;
                    break;
                }


                case RULE_COND_HASMIMETYPE: {
                    ptr = strchr(condition->string[0], '/');
                    if (ptr) {
                        *ptr = ' ';
                    }

                    GetMimeStructure(client);
                    //GET_MIME_STRUCTURE(client, ccode);

                    for (used = 0; used < BongoArrayCount(client->MimeStructure); used++) {
                        ptr = BongoArrayIndex(client->MimeStructure, char *, used);

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

                    GetMimeStructure(client);
                    //GET_MIME_STRUCTURE(client, ccode);

                    result = TRUE;
                    for (used = 0; used < BongoArrayCount(client->MimeStructure); used++) {
                        ptr = BongoArrayIndex(client->MimeStructure, char *, used);

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
                        Log(LOG_DEBUG, "User %s delete rule", client->recipient);

                        /* not really, but this gets us around the write below */
                        written = TRUE;

                        break;
                    }

                    case RULE_ACT_FORWARD: {
                        Log(LOG_DEBUG, "User %s forward rule to %s", client->recipient, action->string[0]);

                        client->flags &= ~(DSN_SUCCESS | DSN_TIMEOUT | DSN_FAILURE | DSN_HEADER | DSN_BODY);
                        client->flags |= NO_RULEPROCESSING;

                        ccode = NMAPSendCommandF(client->conn, "QMOD TO %s %s %lu\r\n", action->string[0], client->recipient, (unsigned long)client->flags);
                        written = TRUE;
                        break;
                    }

                    case RULE_ACT_COPY: {
                        Log(LOG_DEBUG, "User %s copy rule to %s", client->recipient, action->string[0]);

                        client->flags &= ~(DSN_SUCCESS | DSN_TIMEOUT | DSN_FAILURE | DSN_HEADER | DSN_BODY);
                        client->flags |= NO_RULEPROCESSING;

                        ccode = NMAPSendCommandF(client->conn, "QMOD TO %s %s %lu\r\n", action->string[0], client->recipient, (unsigned long)client->flags);
                        written = TRUE;
                        break;
                    }

                    case RULE_ACT_MOVE: {
                        owner = NULL;
                        ptr = NULL;
                        used = 0;

                        ptr = action->string[0];
                        while ((ptr = strchr(ptr, ' ')) != NULL) {
                            *ptr++ = 0x7F;
                        }

                        /* bongo doesn't really have shared folders currently.  skip this stuff */
                        Log(LOG_DEBUG, "moving %s %s", client->recipient, action->string[0]);

                        client->flags |= NO_RULEPROCESSING;
                        ccode = NMAPSendCommandF(client->conn, "QMOD MAILBOX %s %s %lu %s\r\n", client->recipient, client->recipient, (unsigned long)client->flags, action->string[0]);
                        written = TRUE;

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
                        ccode = 0;
                        
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

    if (written == FALSE) {
        /* should probably do this differently to modify flags... */
        ConnWriteF(client->conn, "QMOD RAW %s\r\n", client->envelopeLine);
    }

    return(ccode);
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
ParseRules(RulesClient *client)
{
    unsigned char *cur;
    unsigned char *ptr;
    unsigned char *limit;
    unsigned long used;
    BOOL parsed = FALSE;
    BongoRuleAction *action = NULL;
    BongoRuleCondition *condition = NULL;
    BongoRule *rule = NULL;

    /* sort the array by rule id... */
    BongoArraySort(client->RulesStrings, CompareRuleID);

    for (used = 0; used < BongoArrayCount(client->RulesStrings); used++) {
        cur = BongoArrayIndex(client->RulesStrings, unsigned char *, used);
        ptr = cur;
        limit = cur + strlen(cur);

        if ((limit > cur) && ((limit - cur) >= 27)) {
            if (cur[8] == RULE_ACTIVE) {
                parsed = TRUE;

                rule = (BongoRule *)MemMalloc(sizeof(BongoRule));
            } else {
                continue;
            }
        } else {
            Log(LOG_ERROR, "User %s rule %lu is truncated! %s", client->recipient, used, cur);

            continue;
        }

        if (rule) {
            memset(rule, 0, sizeof(BongoRule));

            rule->active = TRUE;
            rule->string = cur;
            memcpy(rule->id, rule->string, 8);
        } else {
            Log(LOG_ERROR, "Out of memory");

            parsed = FALSE;
            break;
        }

        /* from here on out i need to use ptr where cur is */
        ptr += 9;
        while (parsed && (ptr < limit)) {
            if (!condition) {
                condition = (BongoRuleCondition *)MemMalloc(sizeof(BongoRuleCondition));
                if (condition) {
                    memset(condition, 0, sizeof(BongoRuleCondition));
                } else {
                    Log(LOG_ERROR, "out of memory");

                    parsed = FALSE;
                    break;
                }
            }

            switch (*ptr) {
                case RULE_COND_ANY:
                case RULE_COND_FREE: 
                case RULE_COND_FREENOT: {
                    /* no arguments */
                    if (memcmp(ptr + 1, "000Z000Z", 8) == 0) {
                        condition->type = *ptr;

                        ptr += 9;

                        if (ptr < limit) {
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
                    condition->type = *ptr;
                    condition->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    condition->string[0] = ptr;

                    ptr += condition->length[0];
                    if (memcmp(ptr, "Z000Z", 5) == 0) {
                        ptr[0] = '\0';
                        ptr += 5;

                        if (ptr < limit) {
                            break;
                        }
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_HEADER: 
                case RULE_COND_HEADER_NOT: {
                    /* first argument is the header field; second argument is the search string */
                    condition->type = *ptr;
                    condition->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    condition->string[0] = ptr;

                    ptr += condition->length[0];
                    if ((ptr < (limit - 5)) && (*ptr == 'Z')) {
                        *ptr++ = '\0';

                        condition->length[1] = ((ptr[0] - '0') * 100) + ((ptr[1] - '0') * 10) + (ptr[2] - '0');

                        ptr += 3;
                        condition->string[1] = ptr;

                        ptr += condition->length[1];
                        if ((ptr < (limit - 1)) && (*ptr == 'Z')) {
                            *ptr++ = '\0';

                            break;
                        }
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_SIZE_MORE: 
                case RULE_COND_SIZE_LESS: {
                    /* sole argument is a size in kilobytes */
                    condition->type = *ptr;
                    condition->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    condition->string[0] = ptr;

                    ptr += condition->length[0];
                    if ((ptr < (limit - 5)) && (*ptr == 'Z') && (memcmp(ptr + 1, "000Z", 4) == 0)) {
                        ptr[0] = '\0';
                        ptr += 5;

                        condition->count[0] = atol(condition->string[0]) * 1024;

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_TIMEOFDAYLESS: 
                case RULE_COND_TIMEOFDAYMORE: {
                    /* sole argument is a count in seconds */
                    condition->type = *ptr;
                    condition->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    condition->string[0] = ptr;

                    ptr += condition->length[0];
                    if ((ptr < (limit - 5)) && (*ptr == 'Z') && (memcmp(ptr + 1, "000Z", 4) == 0)) {
                        ptr[0] = '\0';
                        ptr += 5;

                        condition->count[0] = atol(condition->string[0]);

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_WEEKDAYIS: {
                    /* sole argument is a numerical day of week; 0 = sunday */
                    condition->type = *ptr;
                    condition->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    condition->string[0] = ptr;

                    ptr += condition->length[0];
                    if ((ptr < (limit - 5)) 
                            && (memcmp(ptr, "Z000Z", 5) == 0) 
                            && (condition->string[0][0] >= '0')  
                            && (condition->string[0][0] <= '6')) {
                        ptr[0] = '\0';
                        ptr += 5;

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
                    condition->type = *ptr;
                    condition->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    condition->string[0] = ptr;

                    ptr += condition->length[0];
                    if ((ptr < (limit - 5)) 
                            && (*ptr == 'Z') 
                            && (memcmp(ptr + 1, "000Z", 4) == 0) 
                            && (atol(condition->string[0]) >= 0) 
                            && (atol(condition->string[0]) <= 31)) {
                        ptr[0] = '\0';
                        ptr += 5;

                        condition->count[0] = atol(condition->string[0]);

                        break;
                    }

                    parsed = FALSE;
                    break;
                }

                case RULE_COND_MONTHIS: 
                case RULE_COND_MONTHISNOT: {
                    /* sole argument is a month */
                    condition->type = *ptr;
                    condition->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    condition->string[0] = ptr;

                    ptr += condition->length[0];
                    if ((ptr < (limit - 5)) 
                            && (*ptr == 'Z') 
                            && (memcmp(ptr + 1, "000Z", 4) == 0) 
                            && (atol(condition->string[0]) >= 0) 
                            && (atol(condition->string[0]) <= 12)) {
                        ptr[0] = '\0';
                        ptr += 5;

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
                switch (*ptr) {
                    case RULE_ACT_BOOL_AND: 
                    case RULE_ACT_BOOL_OR: 
                    case RULE_ACT_BOOL_NOT: {
                        condition->operand = *ptr++;

                        RuleAppendCondition(rule, condition);
                        condition = NULL;

                        continue;
                    }

                    default: {
                        RuleAppendCondition(rule, condition);
                        condition = NULL;

                        break;
                    }
                }
            }

            break;
        }

        while (parsed && (ptr < limit)) {
            if (!action) {
                action = (BongoRuleAction *)MemMalloc(sizeof(BongoRuleAction));
                if (action) {
                    memset(action, 0, sizeof(BongoRuleAction));
                } else {
                    Log(LOG_ERROR, "out of memory");

                    parsed = FALSE;
                    break;
                }
            }

            switch (*ptr) {
                case RULE_ACT_REPLY: 
                case RULE_ACT_FORWARD: 
                case RULE_ACT_COPY: 
                case RULE_ACT_MOVE: {
                    /* sole argument is a filename? (reply), a remote address (forward and copy), or a mailbox (move) */
                    action->type = *ptr;
                    action->length[0] = ((ptr[1] - '0') * 100) + ((ptr[2] - '0') * 10) + (ptr[3] - '0');

                    ptr += 4;
                    action->string[0] = ptr;

                    ptr += action->length[0];
                    if (memcmp(ptr, "Z000Z", 5) == 0) {
                        ptr[0] = '\0';
                        ptr += 5;

                        if (ptr <= limit) {
                            RuleAppendAction(rule, action);
                            action = NULL;

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
                    if (memcmp(ptr + 1, "000Z000Z", 8) == 0) {
                        action->type = *ptr;

                        ptr += 9;

                        if (ptr <= limit) {
                            RuleAppendAction(rule, action);
                            action = NULL;

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
            RuleAppend(client, rule);
            rule = NULL;

            continue;
        }

        Log(LOG_ERROR, "User %s rule %lu cannot be parsed! Offset %lu", client->recipient, used, cur);

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

static void
RulesCleanupInformation(RulesClient *client)
{
    unsigned int x;

    if (client->RulesList.head) {
        BongoRule *cur = client->RulesList.head;
        while(cur) {
            BongoRule *next = cur->next;
            FreeBongoRule(cur);
            cur = next;
        }
        client->RulesList.head = NULL;
        client->RulesList.tail = NULL;
    }

    if (client->RulesStrings) {
        FreeBongoConfiguration(client->UserConfig);
        client->RulesStrings = NULL;
    }

    if (client->MimeStructure) {
        for(x=0;x<BongoArrayCount(client->MimeStructure);x++) {
            MemFree(BongoArrayIndex(client->MimeStructure, char *, x));
        }
        BongoArrayFree(client->MimeStructure, TRUE);
        client->MimeStructure = NULL;
    }

    if (client->envelopeLine) {
        MemFree(client->envelopeLine);
        client->envelopeLine = NULL;
    }
}

static void 
RulesClientFree(void *clientp)
{
    RulesClient *client = clientp;

    if (client->envelope) {
        MemFree(client->envelope);
    }

    RulesCleanupInformation(client);

    MemPrivatePoolReturnEntry(RulesAgent.OutgoingPool, client);
}

static int 
ProcessEntry(void *clientp, Connection *conn)
{
    int ccode;
    char *envelopeLine;
    char *ptr;
    RulesClient *Queue = clientp;
    Queue->conn = conn;

    ccode = BongoQueueAgentHandshake(Queue->conn, Queue->line, Queue->qID, &Queue->envelopeLength, &Queue->messageLength);

    sprintf(Queue->line, "RulesAgent: %s", Queue->qID);
    XplRenameThread(XplGetThreadID(), Queue->line);

    if (ccode == -1) {
        return -1;
    }

    Queue->envelope = BongoQueueAgentReadEnvelope(Queue->conn, Queue->line, Queue->envelopeLength, &Queue->envelopeLines);

    if (Queue->envelope == NULL) {
        return -1;
    }

    /* initialize the json stuff for this user...  this is crap i know.... */
    Queue->rulesConfig[0].type = BONGO_JSON_STRING;
    Queue->rulesConfig[0].source = NULL;
    Queue->rulesConfig[0].destination = &Queue->RulesStrings;
    Queue->rulesConfig[1].type = BONGO_JSON_NULL;
    Queue->rulesConfig[1].source = NULL;
    Queue->rulesConfig[1].destination = NULL;
    Queue->UserConfig[0].type = BONGO_JSON_ARRAY;
    Queue->UserConfig[0].source = "o:rules/a";
    Queue->UserConfig[0].destination = &Queue->rulesConfig;
    Queue->UserConfig[1].type = BONGO_JSON_NULL;
    Queue->UserConfig[1].source = NULL;
    Queue->UserConfig[1].destination = NULL;

    envelopeLine = Queue->envelope;
    while (*envelopeLine) {
        unsigned char *eol = envelopeLine + strlen(envelopeLine);

        switch(*envelopeLine) {
        case QUEUE_FROM :
            ConnWriteF(Queue->conn, "QMOD RAW %s\r\n", envelopeLine);
            envelopeLine++;
            Queue->authSender = strchr(envelopeLine, ' ');
            if (Queue->authSender) {
                *(Queue->authSender++) = '\0';
                Queue->sender = envelopeLine;
            }
            break;
        case QUEUE_RECIP_LOCAL:
            /* clean up an previous run */
            RulesCleanupInformation(Queue);

            /* isolate out any flags */
            ptr = strrchr(envelopeLine, ' ');
            Queue->flags = (ptr) ? atoi(ptr+1) : 0;

            if (Queue->flags & NO_RULEPROCESSING) {
                ConnWriteF(Queue->conn, "QMOD RAW %s\r\n", envelopeLine);
                break;
            }

            Queue->envelopeLine = MemStrdup(envelopeLine);

            /* increment past the L */
            envelopeLine++;

            /* isolate out the recipient address */
            ptr = strchr(envelopeLine, ' ');
            if (ptr) {
                *ptr = '\0';
                Queue->recipient = envelopeLine;
            }

            /* i need to read out this user's rules from the server and parse them */
            snprintf(Queue->line, CONN_BUFSIZE, "rules/%s", Queue->recipient);
            if (!ReadBongoConfiguration(Queue->UserConfig, Queue->line)) {
            	ConnWriteF(Queue->conn, "QMOD RAW %s\r\n", Queue->envelopeLine);
                break;
            }

            /* now i need to parse all those rules and execute them */
            ParseRules(Queue);
            if (Queue->RulesList.head) {
                ccode = ProcessRules(Queue);
            }
            break;
        default:
            ConnWriteF(Queue->conn, "QMOD RAW %s\r\n", envelopeLine);
            envelopeLine++;
            break;
        }

        /* i could do this a bit more optimized by doing the ENVELOPE_NEXT loop myself, but i'd like
         * to stick to the standard macros */
        envelopeLine = eol;
        BONGO_ENVELOPE_NEXT(envelopeLine);
    }

    /* The caller will call the free function specified on agent init
     * which will free the queue struct.  the caller will then free the
     * connection and do the qdone */
    return 0;
}

static void 
RulesAgentServer(void *ignored)
{
    int minThreads;
    int maxThreads;
    int minSleep;

    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");
    BongoQueueAgentGetThreadPoolParameters(&RulesAgent.agent, &minThreads, &maxThreads, &minSleep);

    /* Listen for incoming queue items.  Call ProcessEntry with a
     * RulesAgentClient allocated for each incoming queue entry. */
    //RulesAgent.OutgoingThreadPool = BongoThreadPoolNew(AGENT_NAME " Clients", BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE, 0, 1, minSleep);
    //RulesAgent.OutgoingThreadPool = BongoThreadPoolNew(AGENT_NAME " Clients", BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE, minThreads, maxThreads, minSleep);
    RulesAgent.OutgoingThreadPool = BongoThreadPoolNew(AGENT_NAME " Clients", 0, minThreads, maxThreads, minSleep);
    RulesAgent.nmapOutgoing = BongoQueueConnectionInit(&RulesAgent.agent, Q_RULE);
    BongoQueueAgentListenWithClientPool(&RulesAgent.agent,
                                       RulesAgent.nmapOutgoing,
                                       RulesAgent.OutgoingThreadPool,
                                       sizeof(RulesClient),
                                       RulesClientFree, 
                                       ProcessEntry,
                                       &RulesAgent.OutgoingPool);
    BongoThreadPoolShutdown(RulesAgent.OutgoingThreadPool);
    ConnClose(RulesAgent.nmapOutgoing, 1);
    RulesAgent.nmapOutgoing = NULL;

    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");

    BongoAgentShutdown(&RulesAgent.agent);

    XplConsolePrintf(AGENT_NAME ": Shutdown complete\r\n");
}

static BOOL 
ReadConfiguration(void)
{
    if (! ReadBongoConfiguration(GlobalConfig, "global")) {
        return FALSE;
    }

    return TRUE;
}

static void 
SignalHandler(int sigtype) 
{
    BongoAgentHandleSignal(&RulesAgent.agent, sigtype);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int startupOpts;
    ConnSSLConfiguration sslconfig;

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n" AGENT_NAME ": exiting.\n", MsgGetUnprivilegedUser());
        return -1;
    }
    XplInit();

    strcpy(RulesAgent.nmapAddress, "127.0.0.1");

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_CONNIO | BA_STARTUP_NMAP;
    ccode = BongoAgentInit(&RulesAgent.agent, AGENT_NAME, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }
    CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL);

    ReadConfiguration();
    sslconfig.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
    sslconfig.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
    sslconfig.key.type = GNUTLS_X509_FMT_PEM;
    RulesAgent.SSL_Context = ConnSSLContextAlloc(&sslconfig);

    XplSignalHandler(SignalHandler);

    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, RulesAgentServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
