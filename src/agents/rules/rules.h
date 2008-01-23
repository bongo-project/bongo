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

#ifndef _BONGORULES_H
#define _BONGORULES_H

#include <connio.h>
#include <msgapi.h>
#include <streamio.h>
#include <nmap.h>
#include <nmlib.h>

#define PRODUCT_SHORT_NAME "bongorules.nlm"

#define CONNECTION_TIMEOUT (15 * 60)

#define RULESRV_USER_RULES (1 << 0)
#define RULESRV_SYSTEM_RULES (1 << 1)

#define RULE_APPEND_CONDITION(r, c) \
            if (((c)->prev = (r)->conditions.tail) != NULL) { \
                (c)->prev->next = (c); \
            } else { \
                (r)->conditions.head = (c); \
            } \
            (r)->conditions.tail = (c); \
            (c) = NULL;

#define RULE_APPEND_ACTION(r, a) \
            if (((a)->prev = (r)->actions.tail) != NULL) { \
                (a)->prev->next = (a); \
            } else { \
                (r)->actions.head = (a); \
            } \
            (r)->actions.tail = (a); \
            (a) = NULL;

#define RULE_APPEND(c, r) \
            if (((r)->prev = (c)->rules.tail) != NULL) { \
                (r)->prev->next = (r); \
            } else { \
                (c)->rules.head = (r); \
            } \
            (c)->rules.tail = (r); \
            (r) = NULL;

#define GET_MIME_STRUCTURE(c, r) \
            if (!(c)->mime) { \
                (c)->mime = MDBCreateValueStruct(Rules.handle.directory, NULL); \
                if ((c)->mime) { \
                    if (((r) = NMAPSendCommandF((c)->conn, "QMIME %s\r\n", (c)->queueID)) != -1) { \
                        while (((r) = NMAPReadAnswer((c)->conn, (c)->line, CONN_BUFSIZE, FALSE)) == 2002) { \
                            MDBAddValue((c)->line, (c)->mime); \
                        } \
                    } \
                } \
            }

#define GET_MIME_SIZES(p, s, l, h) \
            { \
                unsigned char *eof = strrchr((p), '\"'); \
                if (eof) { \
                    eof++; \
                    sscanf(eof, " %lu %lu %lu", &(s), &(l), &(h)); \
                } else { \
                    sscanf((p), "%*u %*s %*s %*s %*s %*s %lu %lu %lu", &(s), &(l), &(h)); \
                } \
            }

typedef enum _RulesStates {
    RULES_STATE_STARTING = 0, 
    RULES_STATE_INITIALIZING, 
    RULES_STATE_LOADING, 
    RULES_STATE_RUNNING, 
    RULES_STATE_RELOADING, 
    RULES_STATE_UNLOADING, 
    RULES_STATE_STOPPING, 
    RULES_STATE_DONE, 

    RULES_STATE_MAX_STATES
} RulesStates;

typedef enum _RulesFlags {
    RULES_FLAG_USER_RULES = (1 << 0), 
    RULES_FLAG_SYSTEM_RULES = (1 << 1), 
} RulesFlags;

typedef enum _RulesClientFlags {
    RULES_CLIENT_FLAG_NEW = (1 << 0), 
    RULES_CLIENT_FLAG_WAITING = (1 << 1), 
    RULES_CLIENT_FLAG_EXITING = (1 << 2), 
    RULES_CLIENT_FLAG_ICAL_CHECKED = (1 << 3), 
    RULES_CLIENT_FLAG_FREE_CHECKED = (1 << 3)
} RulesClientFlags;

typedef struct _BongoRuleCondition {
    struct _BongoRuleCondition *prev;
    struct _BongoRuleCondition *next;

    unsigned char type;
    unsigned char operand;
    unsigned char *string[2];

    unsigned long length[2];
    unsigned long count[2];
} BongoRuleCondition;

typedef struct _BongoRuleAction {
    struct _BongoRuleAction *prev;
    struct _BongoRuleAction *next;

    unsigned char type;
    unsigned char *string[2];

    unsigned long length[2];
} BongoRuleAction;

typedef struct _BongoRule {
    struct _BongoRule *prev;
    struct _BongoRule *next;

    unsigned char *string;
    unsigned char id[9];

    BOOL active;

    struct {
        BongoRuleCondition *head;
        BongoRuleCondition *tail;
    } conditions;

    struct {
        BongoRuleAction *head;
        BongoRuleAction *tail;
    } actions;
} BongoRule;

typedef struct {
    RulesClientFlags flags;

    Connection *conn;
    Connection *store;

    unsigned char *envelope;
    unsigned char line[CONN_BUFSIZE + 1];
    unsigned char command[CONN_BUFSIZE + 1];
    unsigned char queueID[16];
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char user[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char recipient[MDB_MAX_OBJECT_CHARS + 1];

    MDBValueStruct *mime;

#if CAL_IMPLEMENTED
    struct {
        int length;
        unsigned char *data;

        ICalObject *object;
    } iCal;
#endif

    struct sockaddr_in nmap;

    struct {
        BongoRule *head;
        BongoRule *tail;
    } rules;
} RulesClient;

typedef struct _RulesGlobals {
    RulesStates state;
    RulesFlags flags;

    unsigned char officialName[MAXEMAILNAMESIZE + 1];
    unsigned char postmaster[MDB_MAX_OBJECT_CHARS + 1];

    struct {
        MDBHandle directory;

        void *logging;
    } handle;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        XplSemaphore semaphore;

        struct {
            XplSemaphore todo;

            XplAtomic maximum;
            XplAtomic active;
            XplAtomic idle;

            Connection *head;
            Connection *tail;
        } worker;

        struct {
            BOOL enable;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        Connection *conn;

        void *pool;

        time_t sleepTime;

        unsigned long queue;

        unsigned char address[80];
        unsigned char hash[NMAP_HASH_SIZE];
    } nmap;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        XplAtomic active;
    } server;

    struct {
        XplAtomic called;
    } stats;

    struct {
        unsigned char count;
        unsigned char **rules;
    } system;
} RulesGlobals;

extern RulesGlobals Rules;

/* stream.c */
BOOL MWHandleNamedTemplate(void *ClientIn, unsigned char *TemplateName, void *ObjectData);
int RulesStreamToMemory(StreamStruct *codec, StreamStruct *next);
int RulesStreamToFile(StreamStruct *codec, StreamStruct *next);
int RulesStreamFromNMAP(StreamStruct *codec, StreamStruct *next);

#endif /* _BONGORULES_H */
