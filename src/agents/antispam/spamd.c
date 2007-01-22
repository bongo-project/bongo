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

/** \file spamd.c Code used by the anti-spam agent to use the spamd engine.
 */

#include <config.h>

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <streamio.h>
#include <connio.h>

#include "antispam.h"

#define SPAMD_DEFAULT_ADDRESS "127.0.0.1"
#define SPAMD_DEFAULT_PORT 783
#define SPAMD_DEFAULT_WEIGHT 1
#define SPAMD_DEFAULT_HEADER_THRESHOLD -9999
#define SPAMD_DEFAULT_DROP_THRESHOLD 9999
#define SPAMD_DEFAULT_CONNECTION_TIMEOUT 20 /* milliseconds */


#define SPAMD_DEFAULT_FEEDBACK_ADDRESS "127.0.0.1"
#define SPAMD_DEFAULT_FEEDBACK_PORT 781
#define SPAMD_DEFAULT_FEEDBACK_TIMEOUT (60 * 15) /* seconds */

__inline static void
SendFeedback(Feedback *feedback, const char *event, const char *queueId, unsigned long senderIp, char *senderUserName, double score)
{
    struct in_addr addr;
    char *sender;
    long len;
    unsigned long bufferLeft;
    time_t startConnectTime;
    long connectTime;
    
    if (!feedback->enabled) {
        return;
    }

    if (!senderUserName) {
        sender = "-";
    } else {
        sender = senderUserName;
    }

    /* Bug Alert FIXME - senderIP is in non-host order in the envelope for some reason. */ 
    addr.s_addr = senderIp;

    XplMutexLock(feedback->mutex);

    if (sizeof(feedback->buffer) > feedback->bufferUsed) {
        bufferLeft = (unsigned long)(sizeof(feedback->buffer) - feedback->bufferUsed);
    } else {
        bufferLeft = 0;
    }

    len = snprintf(feedback->buffer + feedback->bufferUsed, bufferLeft, "%s %s %d.%d.%d.%d %s %f\r\n", 
                   event, queueId + 4, addr.s_net, addr.s_host, addr.s_lh, addr.s_impno, sender, score);
        
    if ((len < 0) || ((unsigned long)len >= bufferLeft)) {
        feedback->bufferUsed = 0;
        XplMutexUnlock(feedback->mutex);
        XplConsolePrintf("BongoAntispam: (%s:%d) Feedback buffer full. Discarding buffered feedback!\n", __FILE__, __LINE__);
        return;
    }

    feedback->bufferUsed += len;

    if (feedback->serverInUse) {
        XplMutexUnlock(feedback->mutex);
        return;
    }

    feedback->serverInUse = TRUE;

    if (feedback->server.bufferUsed == 0) {
        /* copy the stuff we have to the server buffer and let the mutex go */
        memcpy(feedback->server.buffer, feedback->buffer, feedback->bufferUsed);
        feedback->server.bufferUsed = feedback->bufferUsed;
        feedback->bufferUsed = 0;
    } else {
        /* the server buffer already has stuff in it */
    }

    XplMutexUnlock(feedback->mutex);
        
    /* this might block,  but that is not a problem because the mutex has been released. */
    if (ConnWrite(feedback->server.conn, feedback->server.buffer, feedback->server.bufferUsed) > -1) {
        if (ConnFlush(feedback->server.conn) > -1) {
            feedback->server.bufferUsed = 0;
            feedback->server.timeWaiting = 0;
            feedback->serverInUse = FALSE;
            return;
        }
    }

    /* try again */
    ConnClose(feedback->server.conn, 0);
    startConnectTime = time(NULL);
    if (ConnConnect(feedback->server.conn, NULL, 0, NULL) != -1) {
        if (ConnWrite(feedback->server.conn, feedback->server.buffer, feedback->server.bufferUsed) > -1) {
            if (ConnFlush(feedback->server.conn) > -1) {
                feedback->server.bufferUsed = 0;
                feedback->server.timeWaiting = 0;
                feedback->serverInUse = FALSE;
                return;
            }
        }
    }

    connectTime = time(NULL) - startConnectTime;
    if (connectTime < 2) {
        XplDelay(1000);
        feedback->server.timeWaiting++;
    } else {
        feedback->server.timeWaiting += connectTime;
    }
    if (feedback->server.timeWaiting < feedback->server.timeOut) {
        /* let another thread try */
        feedback->serverInUse = FALSE;
        return;
    }

    /* There have been too many errors; shut down the feedback system */
    ConnFree(feedback->server.conn);
    feedback->server.conn = NULL;
    feedback->enabled = FALSE;
    /* feedback->serverInUse is intentionally left TRUE so that threads */
    /* that have already found feedback->enabled to be true and are waiting */
    /* on the mutex will fall out and not try to use the connection */
}

/** Connects to spamd to process the message.  Depending on your settings, analyzed
 * messages are left untouched, or requeued with additional spam information in the
 * headers, or they may be deleted from the queue entirely.
 *
 * \param queueID is the queue-unique id of the message currently being processed.
 * NMAP provides this in the callback.
 *
 * \param client contains all the information for this particular session with NMAP.
 * 
 * \retval infected: returns a boolean summarizing whether or not the message was
 * determined to be spam.
 *
 * If you want verbose output from spamd portion of the antispam agent then 
 * #define VERBOSE_SPAMD
 */
#define VERBOSE_SPAMD
BOOL
SpamdCheck(SpamdConfig *spamd, ASpamClient *client, const char *queueID, BOOL hasFlags, unsigned long msgFlags, unsigned long senderIp, char *senderUserName)
{   
    int ccode;
    BOOL infected;
    BOOL success;
    double score = 0;
    Connection *conn; /* Holds the current connection to spamd. */

#ifdef VERBOSE_SPAMD
    XplConsolePrintf("AntiSpam: (%s) CheckSpamd\n", queueID);
#endif
    
    infected = FALSE;
    conn = ConnAddressPoolConnect(&(spamd->hosts), spamd->connectionTimeout);
    if (conn) {
        char *ptr;
        char *ptr2;
        unsigned long size;
            
        size = 0;
            
        if (((ccode = NMAPSendCommandF(client->conn, "QRETR %s MESSAGE\r\n", queueID)) != -1)
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1)
            && ccode == 2023) {
            /* Successfully retrieved the message body. Need to parse out the size. */

            ptr = strchr (client->line, ' ');
            if (ptr) {
                *ptr = '\0';
            }
                
            size = atol(client->line);
        }

        if (   (size != 0)
               && ((ccode =  ConnWrite(conn, "PROCESS SPAMC/1.3\r\n", 19)        ) != -1)
               && ((ccode = ConnWriteF(conn, "Content-length: %ld\r\n\r\n", size)) != -1)
               && ((ccode =  ConnFlush(conn)                                     ) != -1)
               /* Write the spamc protocol header to the connection. */
               && ((ccode = ConnReadToConn(client->conn, conn, size)             ) != -1)
               && ((ccode = ConnFlush(conn)                                      ) != -1)
               /* Write the message body to the connection. */
               && ((ccode = ConnReadAnswer(conn, client->line, CONN_BUFSIZE)     ) != -1)) {
            /* Read response from spamd. */
#ifdef VERBOSE_SPAMD
            XplConsolePrintf("AntiSpam: (%s) Answer from socket: \"%s\"\n", queueID, client->line);
#else
            ;
#endif
        } else {
            goto ErrConnFree;
        }

        if (   ((ptr  = strchr(client->line, ' ')) != NULL)
               && ((ptr2 = strchr(ptr,          ' ')) != NULL)) {
            /* Correct response from spamd is "SPAMD/1.1 0 EX_OK", look for the '0'
             * to confirm success. 
             */
            ++ptr;
            *ptr2++ = '\0';
            ccode = atol(ptr);
        } else {
            goto ErrConnFree;
        }

        if (ccode == 0) {
            /* Now we need to parse the header for the spam score. The response header will be
             * of the form:
             *  SPAMD/1.1 0 EX_OK
             *  Content-length: 1018             <-----------------------------+
             *  Spam: True ; 998.8 / 5.0                                       |
             *                                                                 |
             * We have already read the first line, so we are on this line here. We need to
             * grab the content length, and then the score.
             */
		
            size = 0;
            while ((ccode = ConnReadAnswer(conn, client->line, CONN_BUFSIZE)) != -1) {
                if (client->line[0] == '\0') {
                    /* Looking for the blank line separating the spamd protocol header from
                     * the message.
                     */
                    if (size == 0) {
                        /* We should have parsed the size and score before hitting the blank line
                         */
                        goto ErrConnFree;
                    } else {
                        /* Correctly parsed the header.
                         */
                        break;
                    }
                } else if (strncmp(client->line, "Content-length: ", strlen("Content-length: ")) == 0) {
                    /* Looking for the size information.
                     */
                    ptr = client->line + strlen("Content-length: ");
                    size = atol(ptr);
                    if (size == 0) {
                        goto ErrConnFree;
                    }
                } else if (strncmp(client->line, "Spam: ", strlen("Spam: ")) == 0) {
                    /* Looking for the spam score.
                     */
                    ptr = strchr(client->line, ';');
                    score = atof(++ptr);
#ifdef VERBOSE_SPAMD
                    XplConsolePrintf("AntiSpam: (%s) Current message received a spam score of %f\n", queueID, score);
#endif
                }
            }
            if (score >= ASpam.spamd.headerThreshold) {
                infected = TRUE;
                if (score < ASpam.spamd.dropThreshold && size != 0) {
                    /* The spam score is greater than the header threshhold,
                     * but lower than the drop threshhold.  Create a new message
                     * with spam headers added before deleting this one.
                     *
                     * We will use the envelope that we read out in the beginning
                     * to create the new message envelope.
                     */
                    if (   ((ccode = NMAPSendCommandF(client->conn, "QCREA %d\r\n", Q_INCOMING)      ) != -1 )
                           && ((ccode = NMAPReadAnswer  (client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000)
                           /* Create the new queue entry. */
                        ) {
                        ;
                    } else {
#ifdef VERBOSE_SPAMD
                        XplConsolePrintf("AntiSpam: (%s) error while creating new queue entry.\n", queueID);
#endif
                        NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
                        goto ErrConnFree;
                    }

                    if (hasFlags) {
                        ;
                    } else if ((((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW X%ld\r\n", MSG_FLAG_SPAM_CHECKED)) != -1)
                                && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000))) {
                        ;
                    } else {
                        NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                        goto ErrConnFree;
                    }


                    ptr = client->envelope;
                    while (*ptr) {
                        /* Parse the old envelope and send all but the FROM line with 
                         * RAW commands, send the from using QSTOR FROM. 
                         */
                        ptr2 = strchr(ptr, '\n');
                        if (ptr2 == NULL){
                            /* error in the envelope structure. */
                            NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                            goto ErrConnFree;
                        }
                        *ptr2 = '\0';
                        switch (*ptr) {
                        case QUEUE_FLAGS: {
                            /* Add our own flag to the envelope structure.
                             */
                            success = (((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW X%ld\r\n", (msgFlags | MSG_FLAG_SPAM_CHECKED))) != -1)
                                       && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000));
                            break;
                        }
                        case QUEUE_DATE: {
                            success = TRUE;
                            break;
                        }
                        default: {
                            success = (((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW %s\n", ptr)         ) != -1)
                                       && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000));
                        }
                        }
                        *ptr2++ = '\n';
                        ptr = ptr2;
                        if (success) {
                            ;
                        } else {
                            NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                            goto ErrConnFree;
                        }
                    }
                    if (  ((ccode = NMAPSendCommandF(client->conn, "QSTOR MESSAGE %ld\r\n",    size)) != -1)
                          &&((ccode =   ConnReadToConn(conn,         client->conn,               size)) != -1)
                          &&((ccode =   NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000)
                          &&((ccode =  NMAPSendCommand(client->conn, "QRUN\r\n",                    6)) != -1)
                          &&((ccode =   NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000)) {
                        /* Copy out the message data from spamd.
                         * Tell nmap to process the new message. 
                         */
#ifdef VERBOSE_SPAMD
                        XplConsolePrintf("AntiSpam: (%s) Created new queue entry with spam headers.\n", queueID);
#else
                        ;
#endif
                    } else {
                        NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                        goto ErrConnFree;
                    }

                    /* Drop original message. */
                    SendFeedback(&(spamd->feedback), "TAGGED", queueID, senderIp, senderUserName, score);
                        
#ifdef VERBOSE_SPAMD
                    XplConsolePrintf("AntiSpam: (%s) Dropped original spam message from the queue.\n", queueID);
#endif
                    ccode = NMAPSendCommandF(client->conn, "QDELE %s\r\n", queueID); /* expect ccode != -1*/
                    ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE); /* expect ccode == 1000 */
                } else {
                    if (!ASpam.quarantineQueue) {
                        /* Drop message. */
                        SendFeedback(&(spamd->feedback), "DROPPED", queueID, senderIp, senderUserName, score);

#ifdef VERBOSE_SPAMD
                        XplConsolePrintf("AntiSpam: (%s) Dropped spam message from the queue.\n", queueID);
#endif
                        if ((ccode = NMAPSendCommandF(client->conn, "QDELE %s\r\n", queueID)) != -1) {
                            if ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000) {
                                ;
                            } else {
                                XplConsolePrintf("AntiSpam: (QDELE %s) resulted in %s\n", queueID, client->line);
                            }
                        }
                    } else {
                        /* Quarantine message. */
                        SendFeedback(&(spamd->feedback), "QUARANTINED", queueID, senderIp, senderUserName, score);
                            
#ifdef VERBOSE_SPAMD
                        XplConsolePrintf("AntiSpam: (%s) Moved spam message to quarantine queue %lu.\n", queueID, ASpam.quarantineQueue);
#endif
                        if ((ccode = NMAPSendCommandF(client->conn, "QMOVE %s %03lu\r\n", queueID, ASpam.quarantineQueue)) != -1) {
                            if ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000) {
                                ;
                            } else {
                                XplConsolePrintf("AntiSpam: (QMOVE %s %03lu) resulted in %s\n", queueID, ASpam.quarantineQueue, client->line);
                            }
                        }
                    }
                }
            } else {
                /* Pass message */
                SendFeedback(&(spamd->feedback), "PASSED", queueID, senderIp, senderUserName, score);
                    
#ifdef VERBOSE_SPAMD
                XplConsolePrintf("AntiSpam: (%s) score is less than header threshold", queueID);
#endif
            }
        } else {
            /* Pass message */
            SendFeedback(&(spamd->feedback), "PASSED", queueID, senderIp, senderUserName, score);

#ifdef VERBOSE_SPAMD
            XplConsolePrintf("AntiSpam: (%s) Recieved a non-zero status code from spamd: %d", queueID, ccode);
#endif
        }

    ErrConnFree:
        ConnFree(conn);
        return infected;
    }

    XplConsolePrintf("BONGOANTISPAM: failed to connecto to spamd server!\n");
    return(FALSE);
}

static void
ParseHost(char *buffer, char **host, unsigned short *port, unsigned long *weight)
{
    char *portPtr;
    char *weightPtr;

    portPtr = NULL;
    weightPtr = NULL;


    if (*buffer == '\0') {
        return;
    }

    if (*buffer == ':') {
        portPtr = buffer;
    } else {
        *host = buffer;
        portPtr = strchr(buffer + 1, ':');        
    }
    
    if (portPtr) {
        *portPtr = '\0';
        portPtr++;
        if (*portPtr == ':') {
            weightPtr = portPtr;
        } else if (*portPtr != '\0') {
            *port = (unsigned short)atol(portPtr);
            weightPtr = strchr(portPtr + 1, ':');
        }

        if (weightPtr) {
            weightPtr++;
            if ( *weightPtr != '\0' ) {
                *weight = (unsigned long)atol(weightPtr);
            } /*else, retain preset default weight */
        }
    }
}

static void
SpamdFeedbackInit(Feedback *feedbackHost, char *host, unsigned short port, unsigned long timeOut)
{
    if (!feedbackHost->server.conn) {
        struct hostent *hostResult;

        hostResult = gethostbyname(host);
        if (hostResult) {
            feedbackHost->server.conn = ConnAlloc(TRUE);
            if (feedbackHost->server.conn) {
                feedbackHost->server.conn->socketAddress.sin_family = AF_INET;
                feedbackHost->server.conn->socketAddress.sin_addr.s_addr = *(uint32_t *)(hostResult->h_addr_list[0]);
                feedbackHost->server.conn->socketAddress.sin_port = htons(port);
                feedbackHost->server.timeWaiting = 0;
                feedbackHost->server.timeOut = timeOut;
                XplMutexInit(feedbackHost->mutex);
                return;
            }
        } 
    }
    feedbackHost->enabled = FALSE;
}

/*
 * \c HeaderThreshold: \c <num>  is the minimum score before adding spam
 * headers to the message. Messages with a spam score greater than or
 * equal to this threshhold will have spam headers added to them. These
 * headers can then be used in the rule server.
 *
 * \c DropThreshold: \c <num> is the minimum score before dropping the 
 * message from the queue altogether. Messages with a score greater than
 * or equal to this threshhold will not be delivered.
 *
 */

BOOL
SpamdReadConfiguration(SpamdConfig *spamd)
{
    char *host;
    unsigned short port;
    unsigned long weight;
    unsigned long timeOut;
    MDBValueStruct *config; /* Temporarily holds options from the directory */

    memset(spamd, 0, sizeof(SpamdConfig));
    config = MDBCreateValueStruct(ASpam.handle.directory, MsgGetServerDN(NULL));
    if (!config) {
        return(FALSE);
    }

    /* has the user even added spamd configuration data? */
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_ENABLED, config) == 0) {
        /* nothing is configured.  break out */
        MDBDestroyValueStruct(config);
        return(TRUE);
    }
    /* drop out if we are not enabled */
    if (!atoi(config->Value[0])) {
        spamd->enabled = FALSE;
        MDBDestroyValueStruct(config);
        return(TRUE);
    }

    spamd->enabled = TRUE;

    /* Set up default spamd options. */
    spamd->headerThreshold  = SPAMD_DEFAULT_HEADER_THRESHOLD;
    spamd->dropThreshold    = SPAMD_DEFAULT_DROP_THRESHOLD;
    spamd->quarantineQueue  = 0;
    spamd->connectionTimeout = SPAMD_DEFAULT_CONNECTION_TIMEOUT;

    ConnAddressPoolStartup(&spamd->hosts, 0, 0);

    /* read out the other 7 parameters */
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_TIMEOUT, config)>0) {
        spamd->connectionTimeout = atol(config->Value[0]);
    }
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_THRESHOLD_HEAD, config)>0) {
        spamd->headerThreshold = atof(config->Value[0]);
    }
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_THRESHOLD_DROP, config)>0) {
        spamd->dropThreshold = atof(config->Value[0]);
    }
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_QUARANTINE_Q, config)>0) {
        spamd->quarantineQueue = atol(config->Value[0]);
    }
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_HOST, config)>0) {
        host = SPAMD_DEFAULT_ADDRESS;
        port = SPAMD_DEFAULT_PORT;
        weight = SPAMD_DEFAULT_WEIGHT;
        ParseHost(config->Value[0], &host, &port, &weight);
        ConnAddressPoolAddHost(&(spamd->hosts), host, port, weight);
    }
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_FEEDBACK_HOST, config)>0) {
        if (!(spamd->feedback.server.conn)) {
            host = SPAMD_DEFAULT_FEEDBACK_ADDRESS;
            port = SPAMD_DEFAULT_FEEDBACK_PORT;
            timeOut = SPAMD_DEFAULT_FEEDBACK_TIMEOUT;
            ParseHost(config->Value[0], &host, &port, &timeOut);
            SpamdFeedbackInit(&(spamd->feedback), host, port, timeOut);
        }
    }
    MDBFreeValues(config);
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_SPAMD_FEEDBACK_ENABLED, config)>0) {
        spamd->feedback.enabled = (atoi(config->Value[0])) ? TRUE : FALSE;
    }
    MDBDestroyValueStruct(config);
    
    return(TRUE);
}


void
SpamdShutdown(SpamdConfig *spamd)
{
    spamd->enabled = FALSE;
    ConnAddressPoolShutdown(&(spamd->hosts));

    spamd->feedback.enabled = FALSE;
    if (spamd->feedback.server.conn) {
        ConnFree(spamd->feedback.server.conn);
        XplMutexDestroy(spamd->feedback.mutex);
    }
    memset(spamd, 0, sizeof(SpamdConfig));
}

void
SpamdStartup(SpamdConfig *spamd)
{
    if (spamd->enabled) {
        if (spamd->hosts.addressCount == 0) {
            ConnAddressPoolAddHost(&(spamd->hosts), SPAMD_DEFAULT_ADDRESS, SPAMD_DEFAULT_PORT, SPAMD_DEFAULT_WEIGHT);
        }

        if (spamd->feedback.enabled) {
            if (!spamd->feedback.server.conn) {
                SpamdFeedbackInit(&(spamd->feedback), SPAMD_DEFAULT_FEEDBACK_ADDRESS, SPAMD_DEFAULT_FEEDBACK_PORT, SPAMD_DEFAULT_FEEDBACK_TIMEOUT);
            }
        }
        return;
    }

    SpamdShutdown(spamd);
    return;
}
