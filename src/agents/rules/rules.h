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

#ifndef _RULES_H
#define _RULES_H

#include <connio.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>
#include <xpldns.h>
#include <rulesrv.h>

#define AGENT_NAME "bongorules"

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
    Connection *conn;

    unsigned int envelopeLength;
    unsigned int messageLength;
    unsigned int envelopeLines;
    char *envelope;
    char *sender;           /* pointer into the envelope of the from address */
    char *authSender;       /* pointer into the envelope of the envelope from */
    char *recipient;        /* pointer into the envelope of the current recipient */
    int flags;              /* current recipients flags */
    char line[CONN_BUFSIZE + 1];
    char qID[16];           /* holds the queueid pulled during handshake */

    char *envelopeLine ;    /* only used inside ProcessRules to do the qmod raw... */

    BongoConfigItem rulesConfig[2];
    BongoConfigItem UserConfig[2];

    BongoArray *RulesStrings;
    BongoArray *MimeStructure;

    struct {
        BongoRule *head;
        BongoRule *tail;
    } RulesList;

} RulesClient;

typedef struct _RulesAgentGlobals {
    BongoAgent agent;

    XplThreadID threadGroup;    

    Connection *nmapOutgoing;
    char nmapAddress[80];
    
    void *OutgoingPool;
    BongoThreadPool *OutgoingThreadPool;
    bongo_ssl_context *SSL_Context;
} RulesAgentGlobals;

extern RulesAgentGlobals RulesAgent;

static void ParseRules(RulesClient *client);
static int CompareRuleID(const void *first, const void *second);
void RuleAppendCondition(BongoRule *rule, BongoRuleCondition *condition);
void RuleAppendAction(BongoRule *rule, BongoRuleAction *action);
void RuleAppend(RulesClient *client, BongoRule *rule);
static void FreeBongoRule(BongoRule *rule);
static int ProcessRules(RulesClient *client);
int GetMimeStructure(RulesClient *c);
static void RulesCleanupInformation(RulesClient *client);
void GetMimeSizes(unsigned char *p, unsigned long *s, unsigned long *l, unsigned long *h);
#endif
