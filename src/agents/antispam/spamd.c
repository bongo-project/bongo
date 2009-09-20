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
 * (C) 2007 Patrick Felt
 ****************************************************************************/

/** \file spamd.c Code used by the anti-spam agent to use the spamd engine.
 */
#define _NO_BONGO_GLOBALS

#include <config.h>

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoagent.h>
#include <bongoutil.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <streamio.h>
#include <connio.h>

#include "antispam.h"

#define SPAMD_DEFAULT_ADDRESS "127.0.0.1"
#define SPAMD_DEFAULT_PORT 783
#define SPAMD_DEFAULT_WEIGHT 1
#define SPAMD_DEFAULT_CONNECTION_TIMEOUT 20 /* milliseconds */

/** Connect to spamd to process a message. 
 * Depending on your settings, analyzed messages are left untouched, or
 * requeued with additional spam information in the headers, or they may
 * be deleted from the queue entirely.
 *
 * \param	spamd	SpamD configuration structure
 * \param	client	SpamD client structure representing store session
 * \param 	queueID Queue-unique id of the message currently being processed. NMAP provides this in the callback.
 * \param 	hasFlags	To be documented
 * \param 	msgFlags	To be documented
 * \param	senderUserName	Username of the sending party (FIXME - check this)
 * \return	True if infected.
 *
 * If you want verbose output from spamd portion of the antispam agent then 
 * define VERBOSE_SPAMD
 */
/* #define VERBOSE_SPAMD */
BOOL
SpamdCheck(SpamdConfig *spamd, ASpamClient *client, const char *queueID, BOOL hasFlags, 
		   unsigned long msgFlags)
{   
    int ccode;
    BOOL infected;
    BOOL success;
    double score = 0;
    Connection *conn; /* Holds the current connection to spamd. */

    Log(LOG_DEBUG, "(%s) Checking with spamd", queueID);
    
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
            Log(LOG_DEBUG, "(%s) Answer from socket: '%s'", queueID, client->line);
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
                    Log(LOG_INFO, "(%s) spam score of %f", queueID, score);
                }
            }
            infected = TRUE;
            /* Create a new message with spam headers added before deleting this one.
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
                Log(LOG_DEBUG, "(%s) Error creating new queue entry.");
                NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
                goto ErrConnFree;
            }

            if (hasFlags) {
                ;
            } else if ((((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW X%d\r\n", MSG_FLAG_SPAM_CHECKED)) != -1)
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000))) {
                ;
            } else {
                NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                goto ErrConnFree;
            }

            ptr = client->envelope;
            while (*ptr) {
                switch (*ptr) {
                case QUEUE_FLAGS:
                    /* Add our own flag to the envelope structure.
                     */
                    success = (((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW X%ld\r\n", (msgFlags | MSG_FLAG_SPAM_CHECKED))) != -1)
                               && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000));
                   break;
                case QUEUE_DATE:
                    success = TRUE;
                    break;
                default:
                    success = (((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW %s\n", ptr)         ) != -1)
                               && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000));
                }
                if (success) {
                    ;
                } else {
                    NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                    goto ErrConnFree;
                }
                BONGO_ENVELOPE_NEXT(ptr);
            }
            if (  ((ccode = NMAPSendCommandF(client->conn, "QSTOR MESSAGE %ld\r\n",    size)) != -1)
                  &&((ccode =   ConnReadToConn(conn,         client->conn,               size)) != -1)
                  &&((ccode =   NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000)
                  &&((ccode =  NMAPSendCommand(client->conn, "QRUN\r\n",                    6)) != -1)
                  &&((ccode =   NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 1000)) {
                /* Copy out the message data from spamd.
                 * Tell nmap to process the new message. 
                 */
                Log(LOG_DEBUG, "(%s) Created new queue entry with spam headers.", queueID);
            } else {
                NMAPSendCommand(client->conn, "QABRT\r\n", 7);
                goto ErrConnFree;
            }

            /* Drop original message. */
                    
            Log(LOG_DEBUG, "(%s) Dropped original spam message from the queue", queueID);
            ccode = NMAPSendCommandF(client->conn, "QDELE %s\r\n", queueID); /* expect ccode != -1*/
            ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE); /* expect ccode == 1000 */
#if 0
this is commented out until we determine how we want to do this sort of thing, or even if we would
            if (ASpam.quarantineThreshold) {
                /* Quarantine message. */
                        
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
#endif
        } else {
            /* Pass message */
            Log(LOG_DEBUG, "(%s) Received a non-zero status code from spamd: %d", queueID, ccode);
        }

    ErrConnFree:
        ConnFree(conn);
        return infected;
    }

    Log(LOG_ERROR, "(%s) Failed to connect to the spamd server");
    return(FALSE);
}

static void
ParseHost(char *buffer, char **host, int *port, int *weight)
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

static BongoConfigItem SpamdHostList = { BONGO_JSON_STRING, NULL, &ASpam.spamd.hostlist };

// TODO: other items we might want to configure.
// ASpam.nmap.queue - defaults to Q_INCOMING

static BongoConfigItem SpamdConfigSchema[] = {
	{ BONGO_JSON_INT, "o:timeout/i", &ASpam.spamd.connectionTimeout },
	{ BONGO_JSON_ARRAY, "o:hosts/a", &SpamdHostList},
	{ BONGO_JSON_NULL, NULL, NULL },
};

BOOL
SpamdReadConfiguration(SpamdConfig *spamd)
{
    unsigned int i;

    memset(spamd, 0, sizeof(SpamdConfig));

    /* Set up default spamd options. */
    spamd->connectionTimeout = SPAMD_DEFAULT_CONNECTION_TIMEOUT;
    ConnAddressPoolStartup(&spamd->hosts, 0, 0);

    if (!ReadBongoConfiguration(SpamdConfigSchema, "antispam"))
	return FALSE;

    for (i=0; i < ASpam.spamd.hostlist->len; i++) {
	char *hostitem = g_array_index(ASpam.spamd.hostlist, char*, i);
        char *lHost = MemStrdup(hostitem);
        char *host = NULL;
        int port=SPAMD_DEFAULT_PORT, weight=SPAMD_DEFAULT_WEIGHT;
        ParseHost(lHost, &host, &port, &weight);
        ConnAddressPoolAddHost(&ASpam.spamd.hosts, host, port, weight);
        MemFree(lHost);
    }

    return(TRUE);
}


void
SpamdShutdown(SpamdConfig *spamd)
{
    ConnAddressPoolShutdown(&(spamd->hosts));

    memset(spamd, 0, sizeof(SpamdConfig));
}

void
SpamdStartup(SpamdConfig *spamd)
{
    if (spamd->hosts.addressCount == 0) {
        ConnAddressPoolAddHost(&(spamd->hosts), SPAMD_DEFAULT_ADDRESS, SPAMD_DEFAULT_PORT, SPAMD_DEFAULT_WEIGHT);
    }
}
