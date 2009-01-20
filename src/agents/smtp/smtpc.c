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
// Parts Copyright (C) 2007-2008 Alex Hudson. See COPYING for details.
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

#include "smtpc.h"

SMTPAgentGlobals SMTPAgent = {{0,}, };

static void 
SMTPClientFree(void *clientp)
{
    SMTPClient *client = clientp;

    if (client->envelope) {
        MemFree(client->envelope);
    }

    MemPrivatePoolReturnEntry(client);
}

int RecipientCompare(const void *lft, const void *rgt) {
    int Result = 0;

    RecipStruct *l = (RecipStruct *)(lft);
    RecipStruct *r = (RecipStruct *)(rgt);

    /* the SortField is domain.com\0localpart.  to get the sorting right i need to change the null
     * to something so that i can sort by both domain and email.  that way later on i can skip
     * addresses if they are listed as recipients twice on the same mail */
    //*(l->localPart-1) = ' ';
    //*(r->localPart-1) = ' ';

    Result = strcasecmp(l->SortField, r->SortField);

    //*(l->localPart-1) = '\0';
    //*(r->localPart-1) = '\0';

    return Result;
}

Connection *LookupRemoteMX(RecipStruct *Recipient) {
    unsigned char Host[MAXEMAILNAMESIZE+1];
    XplDns_MxLookup *mx = NULL;
    XplDns_IpList *list = NULL;
    Connection *Result=NULL;

    /* first get the domain */
    *(Recipient->localPart-1) = '\0';
    if (Recipient->SortField[0] == '[') {
        // potential ip address in the host name (e.g. jdoe@[1.1.1.1])
        unsigned char *end;
        end = strchr(Recipient->SortField, ']');
        if (end) {
            *end = '\0';
        } else {
            Recipient->Result = DELIVER_BOGUS_NAME;
            *(Recipient->localPart-1) = '@';
            return Result;
        }
        // FIXME: assumes our DNS routines will accept IP address.
        strncpy(Host, Recipient->SortField+1, MAXEMAILNAMESIZE);
        *end = ']';
    } else {
        strncpy(Host, Recipient->SortField, MAXEMAILNAMESIZE);    
    }

    /* make sure to reset the SortField as it was previously */
    *(Recipient->localPart-1) = '@';

    // Resolve Host using MX routines.
    mx = XplDnsNewMxLookup(Host);
    if (mx->status != XPLDNS_SUCCESS) {
        switch(mx->status) {
            case XPLDNS_SUCCESS:
                break;
            case XPLDNS_TRY_AGAIN:
                Recipient->Result = DELIVER_TIMEOUT;
                goto finish;
                break;
            case XPLDNS_NOT_FOUND:
            case XPLDNS_NO_DATA:
                Recipient->Result = DELIVER_HOST_UNKNOWN;
                goto finish;
                break;
            default:
            case XPLDNS_DNS_ERROR:
                Recipient->Result = DELIVER_TRY_LATER;
                goto finish;
                break;
        }
    }

    while ((list = XplDnsNextMxLookupIpList(mx)) != NULL) {
        int x;
        int status;

        for (x = 0; x < list->number; x++) {
            /* FIXME
            if (Exiting) {
                DELIVER_ERROR(DELIVER_TRY_LATER);
                goto finish;
            }
            */
            // FIXME: check for ETRN, or mail being relayed.
            Result = ConnAlloc(TRUE);
            Result->socketAddress.sin_addr.s_addr = list->ip_list[x];
            Result->socketAddress.sin_family = AF_INET;
            Result->socketAddress.sin_port = htons (25);
            status = ConnConnect(Result, NULL, 0, NULL);
            if (status <= 0) {
                switch (errno) {
                    case ETIMEDOUT:
                        Recipient->Result = DELIVER_TIMEOUT;
                        break;
                    case ECONNREFUSED:
                        Recipient->Result = DELIVER_REFUSED;
                        break;
                    case ENETUNREACH:
                        Recipient->Result = DELIVER_UNREACHABLE;
                        break;
                    default:
                        Recipient->Result = DELIVER_TRY_LATER;
                        break;
                }
                ConnFree(Result);
                Result = NULL;
                continue;
            }
            goto finish;
        }

        if (list) {
            MemFree(list);
            list = NULL;
        }
    }


finish:
    if (list) {
        MemFree(list);
        list = NULL;
    }
    XplDnsFreeMxLookup(mx);

    return Result;
}

inline BOOL
CheckResponse(const char *Response) {
    return FALSE;
};

BOOL
DeliverMessage(SMTPClient *Queue, SMTPClient *Remote, RecipStruct *Recip) {
    int Extensions;
    unsigned long Size=0, MessageLineLen=0;
    long long MessageLength = 0;
    int Ret;
    unsigned char TimeBuf[80];
    char *MessageLine=NULL;

    /* we are connected to the remote smtp server, but we haven't done any conversation yet */
    /*  read out the banner */
    ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
    if (Remote->line[0] != '2') {
        // we can't deliver to this server...
        if (Remote->line[0] == '4') {
            // temporary error
            Recip->Result = DELIVER_TRY_LATER;
            return FALSE;
        }
        Recip->Result = DELIVER_REFUSED;
        return FALSE;
    }
    // read out any additional banner lines
    while(Remote->line[0] == '2' && Remote->line[3] == '-') {
        ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
    }

beginConversation:
    Extensions = 0;
    ConnWriteF(Remote->conn, "EHLO %s\r\n", BongoGlobals.hostname);
    ConnFlush(Remote->conn);
    ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);

    if (Remote->line[0] != '2' ) {
        /* the remote server doesn't seem to support ehlo.  they should upgrade to bongo! */
        ConnWriteF(Remote->conn, "HELO %s\r\n", BongoGlobals.hostname);
        ConnFlush(Remote->conn);
        ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
        if (atoi(Remote->line) != 250) {
            Recip->Result = DELIVER_TRY_LATER;
            return FALSE;
        }
    }

    while(Remote->line[3] == '-') {
        ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
        if (strcmp(&Remote->line[4], "DSN") == 0) {
            Extensions |= EXT_DSN;
        } else if (strcmp(&Remote->line[4], "8BITMIME") == 0) {
            Extensions |= EXT_8BITMIME;
        } else if (strcmp(&Remote->line[4], "STARTTLS") == 0) {
            Extensions |= EXT_TLS;
        } else if (strcmp(&Remote->line[4], "SIZE") == 0) {
            unsigned char *ptr;
            ptr = strchr(&Remote->line[4], ' ');
            if (ptr) {
                Extensions |= EXT_SIZE;
                Size = atol(ptr);
            }
        } else {
            /* we aren't concerned about this extension */
        }
    }

    if ((Extensions & EXT_TLS) && (Remote->conn->ssl.enable == FALSE)) {
        /* start up that tls badboy before doing anything else!! */
        ConnWriteF(Remote->conn, "STARTTLS\r\n");
        ConnFlush(Remote->conn);
        ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
        if (atoi(Remote->line) == 220) {
            if (ConnEncrypt(Remote->conn, SMTPAgent.SSL_Context) < 0) {
                /* if the tls negotiation fails then we've got a pretty serious error */
                Recip->Result = DELIVER_TRY_LATER;
                return FALSE;
            }
            goto beginConversation;
        }
    }

    /* begin the delivery process */
    if ((Extensions & EXT_SIZE) && (Queue->messageLength > Size)) {
        /* the message is too big */
        ConnWrite(Remote->conn, "QUIT\r\n", 6);
        ConnFlush(Remote->conn);
        Recip->Result = DELIVER_FAILURE;
        return FALSE;
    }

    /* mail from */
    Ret = ConnWriteF(Remote->conn, "MAIL FROM: <%s>", Queue->sender);
    if (Extensions & EXT_SIZE) {
        Ret += ConnWriteF(Remote->conn, " SIZE=%u", Queue->messageLength);
    }

    if (Extensions & EXT_DSN) {
        /* were there any dsn requests for this mail? */
        if (Recip->Flags & DSN_BODY) {
            Ret += ConnWriteStr(Remote->conn, " RET=FULL");
        } else if (Recip->Flags & DSN_HEADER) {
            Ret += ConnWriteStr(Remote->conn, " RET=HDRS");
        }

        if (Queue->authSender[0] != '-') {
            /* this is what it appears the old agent did, however it seems there would be better values
             * ref: RFC3461 section 4.4 */
            Ret += ConnWriteF(Remote->conn, " ENVID=%s", Queue->authSender);
        }
    }

    if ((Extensions & EXT_8BITMIME) && (Queue->flags & MSG_FLAG_ENCODING_8BITM)) {
        Ret += ConnWriteStr(Remote->conn, " BODY=8BITMIME");
    }

    /* mail from complete, add the crlf and send it */
    Ret += ConnWriteStr(Remote->conn, "\r\n");
    if (ConnFlush(Remote->conn) != Ret) {
        Recip->Result = DELIVER_TRY_LATER;
        return FALSE;
    }

    ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
    if (atoi(Remote->line) != 250) {
        Recip->Result = DELIVER_TRY_LATER;
        return FALSE;
    }

    /* recip to */
    ConnWriteF(Remote->conn, "RCPT TO: <%s>", Recip->To);

    if (Extensions & EXT_DSN) {
        if (Recip->ORecip) {
            /* apparently some emails can come through with rfc822; on them?? */
            if (strncasecmp(Recip->ORecip, "rfc822;", 7) == 0) {
                /* we are already set up and can just send the value through. possibly a relay? */
                ConnWriteF(Remote->conn, " ORCPT=%s", Recip->ORecip);
            } else {
                /* we need to translate the ORecip to xtext as defined in rfc 1891 */
                unsigned char *ptr = Recip->ORecip;
                unsigned char Hex[] = "012345678ABCDEF";

                ConnWriteStr(Remote->conn, " ORCPT=rfc822;");
                while (*ptr) {
                    switch(*ptr) {
                    case '+':
                    case '=':
                        ConnWriteF(Remote->conn, "+%c%c", Hex[((0xF0 & *ptr) >> 4)], Hex[(0x0F & *ptr)]);
                        break;
                    default:
                        ConnWrite(Remote->conn, ptr, 1);
                        break;
                    }
                    ptr++;
                }
            }
        }

        /* NOTIFY section */
        ConnWriteStr(Remote->conn, " NOTIFY=");
        if (Recip->Flags & (DSN_SUCCESS | DSN_FAILURE | DSN_TIMEOUT)) {
            BOOL started=FALSE;

            if (Recip->Flags & DSN_SUCCESS) {
                ConnWriteStr(Remote->conn, "SUCCESS");
                started = TRUE;
            }

            if (Recip->Flags & DSN_FAILURE) {
                if (started) {
                    ConnWriteStr(Remote->conn, ",");
                }
                ConnWriteStr(Remote->conn, "FAILURE");
                started = TRUE;
            }

            if (Recip->Flags & DSN_TIMEOUT) {
                if (started) {
                    ConnWriteStr(Remote->conn, ",");
                }
                ConnWriteStr(Remote->conn, "DELAY");
            }
        } else {
            /* no notification requested */
            ConnWriteStr(Remote->conn, "NEVER");
        }
    }

    /* terminate this string and flush it */
    ConnWriteStr(Remote->conn, "\r\n");
    ConnFlush(Remote->conn);

    /* read in the response from the rcpt to */
    ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
    /* rather than convert this one to an int i'm going to do a char
     * comparison so that i don't have to do a divide later 
     * according to rfc 2821
     *      250, 251, or 252 are successful deliveries.
     *      4xx are temporary failures and should be re-attempted
     *      5xx are fatal errors */
    Remote->line[3] = '\0';
    if (Remote->line[0] == '5') {
        Log(LOG_INFO, "Message permanantly rejected from %s to %s (flags: %d, status: %s)",
            Queue->sender, Recip->To, Recip->Flags, Remote->line);
        Recip->Result = DELIVER_FAILURE;

        /* TODO: am i supposed to bounce the mail here? */
        return FALSE;
    } else if (Remote->line[0] != '2') {
        /* all other errors will be re-tried later */
        Log(LOG_INFO, "Message temporarily rejected from %s to %s (flags: %d, status: %s)",
            Queue->sender, Recip->To, Recip->Flags, Remote->line);
        Recip->Result = DELIVER_TRY_LATER;
        return FALSE;
    }

    /* delivery accepted, continue on */
    Log(LOG_INFO, "Message accepted from %s to %s (flags: %d, status: %s)",
        Queue->sender, Recip->To, Recip->Flags, Remote->line);

    ConnWriteStr(Remote->conn, "DATA\r\n");
    ConnFlush(Remote->conn);

    ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);
    if (atoi(Remote->line) != 354) {
        Recip->Result = DELIVER_TRY_LATER;
        return FALSE;
    }

    MsgGetRFC822Date(-1, 0, TimeBuf);
    ConnWriteF(Remote->conn, "Received: from %s (%d.%d.%d.%d) by %s\r\n\twith NMAP (bongosmtpc Agent); %s\r\n",
        BongoGlobals.hostname,
        Queue->conn->socketAddress.sin_addr.s_net,
        Queue->conn->socketAddress.sin_addr.s_host,
        Queue->conn->socketAddress.sin_addr.s_lh,
        Queue->conn->socketAddress.sin_addr.s_impno,
        BongoGlobals.hostname,
        TimeBuf);
    
    /* stream the message over */
    ConnWriteF(Queue->conn, "QRETR %s MESSAGE\r\n", Queue->qID);
    ConnFlush(Queue->conn);
    ConnReadAnswer(Queue->conn, Queue->line, CONN_BUFSIZE);
    if (atoi(Queue->line) != 2023) {
        Recip->Result = DELIVER_TRY_LATER;
        return FALSE;
    }

    /* according to rfc2821 section 4.5.2 TransparencyL
     * if the first character of a line is a . insert another */
    MessageLength = atol(&(Queue->line[5]));
    while (MessageLength > 0) {
        ConnReadToAllocatedBuffer(Queue->conn, &MessageLine, &MessageLineLen);
        if (*MessageLine == '.') {
            ConnWriteStr(Remote->conn, ".");
        }
        MessageLength -= ConnWrite(Remote->conn, MessageLine, strlen(MessageLine));
        MessageLength -= ConnWriteStr(Remote->conn, "\r\n");
    }
    if (MessageLine) {
        MemFree(MessageLine);
    }

    ConnWriteStr(Remote->conn, ".\r\n");
    ConnFlush(Remote->conn);
    ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);

    ConnWriteStr(Remote->conn, "QUIT\r\n");
    ConnFlush(Remote->conn);
    ConnReadAnswer(Remote->conn, Remote->line, CONN_BUFSIZE);

    Recip->Result = DELIVER_SUCCESS;
    return TRUE;
}

static int 
ProcessEntry(void *clientp, Connection *conn)
{
    int ccode;
    char *envelopeLine;
    BongoArray *Recipients;
    RecipStruct CurrentRecip;
    SMTPClient *Queue = clientp;
    unsigned int startLocation, Length;

    Queue->conn = conn;

    ccode = BongoQueueAgentHandshake(Queue->conn, Queue->line, Queue->qID, &Queue->envelopeLength, &Queue->messageLength);

    sprintf(Queue->line, "SMTPAgent: %s", Queue->qID);
    XplRenameThread(XplGetThreadID(), Queue->line);

    if (ccode == -1) {
        return -1;
    }

    Queue->envelope = BongoQueueAgentReadEnvelope(Queue->conn, Queue->line, Queue->envelopeLength, &Queue->envelopeLines);

    if (Queue->envelope == NULL) {
        return -1;
    }

    /* array of all the recipients and their information */
    Recipients = BongoArrayNew(sizeof(RecipStruct), Queue->envelopeLines);
    
    /* parse the envelope lines.  since i could be changing the recipients later in the flow
     * i need to write out all lines as i go */
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
        case QUEUE_FLAGS:
            ConnWriteF(Queue->conn, "QMOD RAW %s\r\n", envelopeLine);
            envelopeLine++;
            Queue->flags = atoi(envelopeLine);
            break;
        case QUEUE_DATE:
            ConnWriteF(Queue->conn, "QMOD RAW %s\r\n", envelopeLine);
            envelopeLine++;
            break;
        case QUEUE_RECIP_REMOTE: {
                envelopeLine++;
                memset(&CurrentRecip, 0, sizeof(RecipStruct));
                /* add the recipient into array of recipients */
                unsigned char *orecip, *flags, *ptr;
                CurrentRecip.Flags = 0;
                CurrentRecip.Result = 0;
            
                orecip = strchr(envelopeLine, ' ');
                if (orecip) {
                    *orecip = '\0';
                    orecip++;

                    /* copy out the to */
                    strncpy(CurrentRecip.To, envelopeLine, MAXEMAILNAMESIZE);
                    CurrentRecip.ToLen = strlen(CurrentRecip.To);

                    /* find the flags if any */
                    flags = strchr(orecip, ' ');
                    if (flags) {
                        *flags = '\0';
                        flags++;
                        CurrentRecip.Flags = atoi(flags);
                    }

                    /* copy out the original recipient */
                    strncpy(CurrentRecip.ORecip, orecip, MAXEMAILNAMESIZE);
                } else {
                    /* we should always have an orecip... */
                    CurrentRecip.ORecip[0] = '\0';
                }

                /* reformat to domain_part@local_part for better sorting and searching */
                ptr = strchr(CurrentRecip.To, '@');
                if (ptr) {
                    *ptr = '\0';
                    CurrentRecip.localPart = CurrentRecip.SortField + (strlen(ptr+1)+1); /* should points us to the @ */
                    sprintf(CurrentRecip.SortField, "%s@%s", ptr+1, CurrentRecip.To);
                    *ptr = '@';
                } else {
                    CurrentRecip.SortField[0] = '\0';
                    CurrentRecip.localPart = NULL;
                }

                BongoArrayAppendValue(Recipients, CurrentRecip);
                break;
            }
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

    /* now that we've got all recipients and locations, let's sort them to use connections more efficiently */
    BongoArraySort(Recipients, (ArrayCompareFunc)RecipientCompare);

    /* now i can skip over any duplicates an do lookups once per remote domain */
    for(startLocation=0;startLocation<Recipients->len;startLocation++) {
        SMTPClient Remote;
        RecipStruct NextRecip;
        unsigned char *lft;

        CurrentRecip = BongoArrayIndex(Recipients, RecipStruct, startLocation);
        lft = CurrentRecip.SortField;
        Length=startLocation+1;
        while (Length < Recipients->len) {
            /* skip over duplicates */
            NextRecip = BongoArrayIndex(Recipients, RecipStruct, Length);
            if (strcasecmp(NextRecip.SortField, lft) != 0) {
                startLocation = Length-1; /* i'm going to inc at the end of the loop... */
                break;
            }

            Length++;
        }

        /* by the time i get here, CurrentRecip should be the last recip
         * of any string of duplicates */
        Remote.conn = LookupRemoteMX(&CurrentRecip);
        if (!Remote.conn) {
            /* there was an error looking up or connecting to the remote server
             * if i get an error in this stage of the process i need to just skip
             * the rest of the email addresses on this domain */
            /* FIXME: */
            continue;
        }

        DeliverMessage(Queue, &Remote, &CurrentRecip);
        
        // we're responsible for closing this connection
        ConnClose(Remote.conn, 1);
        ConnFree(Remote.conn);
        Remote.conn = NULL;

        /* now handle delivery result codes */
        switch(CurrentRecip.Result) {
        case DELIVER_SUCCESS:
            if (CurrentRecip.Flags & DSN_SUCCESS) {
                ConnWriteF(Queue->conn, "QRTS %s %s %lu %d\r\n", CurrentRecip.To, CurrentRecip.ORecip, CurrentRecip.Flags, DELIVER_SUCCESS);
                ConnFlush(Queue->conn);
            }
            break;
        case DELIVER_TIMEOUT:
        case DELIVER_REFUSED:
        case DELIVER_UNREACHABLE:
        case DELIVER_TRY_LATER:
            ConnWriteF(Queue->conn, "QMOD RAW R%s %s %lu\r\n", CurrentRecip.To, CurrentRecip.ORecip, CurrentRecip.Flags);
            ConnFlush(Queue->conn);
            break;
        case DELIVER_HOST_UNKNOWN:
        case DELIVER_USER_UNKNOWN:
        case DELIVER_BOGUS_NAME:
        case DELIVER_INTERNAL_ERROR:
        case DELIVER_FAILURE:
            if (CurrentRecip.Flags & DSN_FAILURE) {
                ConnWriteF(Queue->conn, "QRTS %s %s %lu %d\r\n", CurrentRecip.To, CurrentRecip.ORecip, CurrentRecip.Flags, CurrentRecip.Result);
                ConnFlush(Queue->conn);
            }
            break;
        }
    }

    if (Recipients) {
        BongoArrayFree(Recipients, TRUE);
    }
    /* The caller will call the free function specified on agent init
     * which will free the queue struct.  the caller will then free the
     * connection and do the qdone */
    return 0;
}

static void 
SMTPAgentServer(void *ignored)
{
    int minThreads;
    int maxThreads;
    int minSleep;

    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");
    BongoQueueAgentGetThreadPoolParameters(&SMTPAgent.agent, &minThreads, &maxThreads, &minSleep);

    /* Listen for incoming queue items.  Call ProcessEntry with a
     * SMTPAgentClient allocated for each incoming queue entry. */
    SMTPAgent.OutgoingThreadPool = BongoThreadPoolNew(AGENT_NAME " Clients", BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE, 0, 1, minSleep);
    //SMTPAgent.OutgoingThreadPool = BongoThreadPoolNew(AGENT_NAME " Clients", BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE, minThreads, maxThreads, minSleep);
    SMTPAgent.nmapOutgoing = BongoQueueConnectionInit(&SMTPAgent.agent, Q_OUTGOING);
    BongoQueueAgentListenWithClientPool(&SMTPAgent.agent,
                                       SMTPAgent.nmapOutgoing,
                                       SMTPAgent.OutgoingThreadPool,
                                       sizeof(SMTPClient),
                                       SMTPClientFree, 
                                       ProcessEntry,
                                       &SMTPAgent.OutgoingPool);
    BongoThreadPoolShutdown(SMTPAgent.OutgoingThreadPool);
    ConnClose(SMTPAgent.nmapOutgoing, 1);
    SMTPAgent.nmapOutgoing = NULL;

    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");

    BongoAgentShutdown(&SMTPAgent.agent);

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
    BongoAgentHandleSignal(&SMTPAgent.agent, sigtype);
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

    strcpy(SMTPAgent.nmapAddress, "127.0.0.1");

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_CONNIO | BA_STARTUP_NMAP;
    ccode = BongoAgentInit(&SMTPAgent.agent, AGENT_NAME, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    ReadConfiguration();
    sslconfig.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
    sslconfig.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
    sslconfig.key.type = GNUTLS_X509_FMT_PEM;
    SMTPAgent.SSL_Context = ConnSSLContextAlloc(&sslconfig);

    XplSignalHandler(SignalHandler);

    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, SMTPAgentServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
