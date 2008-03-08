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

#ifndef _SMTPC_H
#define _SMTPC_H

#include <connio.h>
#include <msgapi.h>
#include <nmap.h>
#include <nmlib.h>
#include <bongoagent.h>
#include <xpldns.h>

#include "smtp.h"

#define AGENT_NAME "smtpd_c"

typedef struct {
    Connection *conn;

    unsigned int envelopeLength;
    unsigned int messageLength;
    unsigned int envelopeLines;
    char *envelope;
    char *sender;           /* pointer into the envelope of the from address */
    char *authSender;       /* pointer into the envelope of the envelope from */
    int flags;
    char line[CONN_BUFSIZE + 1];
    char qID[16];           /* holds the queueid pulled during handshake */
} SMTPClient;

typedef struct _SMTPAgentGlobals {
    BongoAgent agent;

    XplThreadID threadGroup;    

    Connection *nmapOutgoing;
    char nmapAddress[80];
    
    void *OutgoingPool;
    BongoThreadPool *OutgoingThreadPool;
    bongo_ssl_context *SSL_Context;
} SMTPAgentGlobals;

typedef struct {
    unsigned char To[MAXEMAILNAMESIZE+1];
    unsigned char ORecip[MAXEMAILNAMESIZE+1];
    unsigned char SortField[MAXEMAILNAMESIZE+2]; /* will be formatted domain.com@emailaddr -- used for comparisons and sorting */
    unsigned char *localPart; /* pointer into the SortField for the beginning of the localPart */
    size_t ToLen; /* just so we don't have to calculate the length a bunch of times */
    unsigned long Flags;
    int Result;
} RecipStruct;

extern SMTPAgentGlobals SMTPAgent;

int RecipientCompare(const void *lft, const void *rgt);
Connection *LookupRemoteMX(RecipStruct *Recipient);
BOOL DeliverMessage(SMTPClient *Queue, SMTPClient *Remote, RecipStruct *Recip);

#endif /* _SMTP_O_H */
