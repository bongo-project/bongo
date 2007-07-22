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
 * (C) 2007 Alex Hudson
 ****************************************************************************/

#include <config.h>

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoagent.h>
#include <bongoutil.h>
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <streamio.h>
#include <connio.h>

#include "avirus.h"

#if defined(RELEASE_BUILD)
#define AVClientAlloc() MemPrivatePoolGetEntry(AVirus.nmap.pool)
#else
#define AVClientAlloc() MemPrivatePoolGetEntryDebug(AVirus.nmap.pool, __FILE__, __LINE__)
#endif

static void SignalHandler(int sigtype);

#define QUEUE_WORK_TO_DO(c, id, r) \
        { \
            XplWaitOnLocalSemaphore(AVirus.nmap.semaphore); \
            if (XplSafeRead(AVirus.nmap.worker.idle)) { \
                (c)->queue.previous = NULL; \
                if (((c)->queue.next = AVirus.nmap.worker.head) != NULL) { \
                    (c)->queue.next->queue.previous = (c); \
                } else { \
                    AVirus.nmap.worker.tail = (c); \
                } \
                AVirus.nmap.worker.head = (c); \
                (r) = 0; \
            } else { \
                XplSafeIncrement(AVirus.nmap.worker.active); \
                XplSignalBlock(); \
                XplBeginThread(&(id), HandleConnection, 24 * 1024, XPL_INT_TO_PTR(XplSafeRead(AVirus.nmap.worker.active)), (r)); \
                XplSignalHandler(SignalHandler); \
                if (!(r)) { \
                    (c)->queue.previous = NULL; \
                    if (((c)->queue.next = AVirus.nmap.worker.head) != NULL) { \
                        (c)->queue.next->queue.previous = (c); \
                    } else { \
                        AVirus.nmap.worker.tail = (c); \
                    } \
                    AVirus.nmap.worker.head = (c); \
                } else { \
                    XplSafeDecrement(AVirus.nmap.worker.active); \
                    (r) = -1; \
                } \
            } \
            XplSignalLocalSemaphore(AVirus.nmap.semaphore); \
        }


AVirusGlobals AVirus;

static BOOL 
AVClientAllocCB(void *buffer, void *data)
{
    register AVClient *c = (AVClient *)buffer;

    memset(c, 0, sizeof(AVClient));

    return(TRUE);
}

static void 
AVClientFree(AVClient *client)
{
    register AVClient *c = client;

    if (c->conn) {
        ConnClose(c->conn, 1);
        ConnFree(c->conn);
        c->conn = NULL;
    }

    if (c->uservs) {
        MDBDestroyValueStruct(c->uservs);
    }

    MemPrivatePoolReturnEntry(c);

    return;
}

static void 
FreeClientData(AVClient *client)
{
    unsigned long i;

    if (client && !(client->flags & AV_CLIENT_FLAG_EXITING)) {
        client->flags |= AV_CLIENT_FLAG_EXITING;

        if (client->conn) {
            ConnClose(client->conn, 1);
            ConnFree(client->conn);
            client->conn = NULL;
        }

        if (client->uservs) {
            MDBFreeValues(client->uservs);

            MDBSetValueStructContext(NULL, client->uservs);
        }

        if (client->envelope) {
            MemFree(client->envelope);
        }

	for (i = 0; i < client->foundViruses.used; i++) {
	    MemFree(client->foundViruses.names[i]);
	}
	MemFree(client->foundViruses.names);

        ClearMIMECache(client);
    }

    return;
}

static __inline BOOL 
ScanMessageParts(AVClient *client, unsigned char *queueID)
{
    BOOL scan;
    int infected;
    int ccode;
    unsigned int used;
    unsigned long i;
    unsigned long id;
    unsigned char *ptr;
    unsigned char *ptr2;

    scan = FALSE;
    ccode = NMAPSendCommandF(client->conn, "QMIME %s\r\n", queueID);
    if ((ccode != -1) 
	&& ((ccode = LoadMIMECache(client)) != -1)) {
        scan = FALSE;
        for (used = 0; used < client->mime.used; used++) {
            switch(toupper(client->mime.cache[used].type[0])) {
	    case 'M': /* multipart & message */
		if (QuickNCmp(client->mime.cache[used].type, "multipart", 9) || QuickNCmp(client->mime.cache[used].type, "message", 7)) {
		    continue;
		}
		break;
	    case 'T': /* Text */
		if (QuickCmp(client->mime.cache[used].type, "text/plain") || QuickCmp(client->mime.cache[used].type, "text/html")) {
		    continue;
		}
		
		break;
            }
	    
            client->mime.cache[used].flags |= AV_FLAG_SCAN;
            scan = TRUE;
        }
	if (!scan) {
	    return 0;
	}
    } else {
	return -1;
    }
    
    infected = 0;

    for (i = 0; i < client->mime.used; i++) {
        if (!(client->mime.cache[i].flags & AV_FLAG_SCAN)) {
            continue;
        }

        client->mime.current = i;
        XplSafeIncrement(AVirus.stats.attachments.scanned);

        XplWaitOnLocalSemaphore(AVirus.id.semaphore);
        id = AVirus.id.next;
        AVirus.id.next++;
        XplSignalLocalSemaphore(AVirus.id.semaphore);

        ptr = strrchr(client->mime.cache[i].fileName, '.');
        if (ptr) {
            ptr2 = ptr + 1;
            while (*ptr2) {
                if (!isalnum(*ptr2)) {
                    *ptr2 = 'x';
                }

                ptr2++;
            }

            sprintf(client->line, "AVScan: %lu%s", id, ptr);
            XplRenameThread(XplGetThreadID(), client->line);

            sprintf(client->work, "%s/%lu%s", AVirus.path.work, id, ptr);
        } else {
            sprintf(client->line, "AV Scan: %lu.exe", id);
            XplRenameThread(XplGetThreadID(), client->line);

            sprintf(client->work, "%s/%lu.exe", AVirus.path.work, id);
        }

        if (StreamAttachmentToFile(client, queueID, &(client->mime.cache[i])) > 0) {
            /*
                Based on the configured scanner, scan the attachment in client->work.

                Set client->mime.cache[i].flags accordingly.
            */
            switch (AVirus.flags & (AV_FLAG_USE_CA | AV_FLAG_USE_MCAFEE | AV_FLAG_USE_SYMANTEC | AV_FLAG_USE_COMMANDAV)) {
                case AV_FLAG_USE_CA: 
                case AV_FLAG_USE_MCAFEE: 
                case AV_FLAG_USE_SYMANTEC: 
                case AV_FLAG_USE_COMMANDAV: {
                    client->mime.cache[i].flags &= ~(AV_FLAG_INFECTED | AV_FLAG_CURED);
		    break;
                }
            }

            /* We've scanned it, do we have a virus? */
            if (client->mime.cache[i].flags & AV_FLAG_INFECTED) {
                XplSafeIncrement(AVirus.stats.viruses);
		infected = 1;		
            }
        }

        unlink(client->work);
    }

    return infected;
}

static __inline int
ScanMessageClam(AVClient *client, char *queueID)
{
    int ccode;
    long size;
    BOOL infected;
    Connection *conn;

    infected = 0;

    conn = ConnAlloc(TRUE);
    if (conn) {
	memcpy(&conn->socketAddress, &AVirus.clam.addr, sizeof(struct sockaddr_in));
	if (ConnConnectEx(conn, NULL, 0, NULL, client->conn->trace.destination) >= 0) {
	    Connection *data;
	    unsigned short port;
	    
	    ConnWrite(conn, "STREAM\r\n", strlen("STREAM\r\n"));
	    ConnFlush(conn);
	    ccode = ConnReadAnswer(conn, client->line, CONN_BUFSIZE);

	    if (!ccode || strncmp(client->line, "PORT ", strlen("PORT ")) != 0 || (port = atoi(client->line + strlen("PORT "))) == 0) {
		    ConnFree(conn);
		    return -1;
	    }
	    
	    data = ConnAlloc(TRUE);
	    if (!data) {
		    ConnFree(conn);
		    return -1;
	    }

	    memcpy(&data->socketAddress, &AVirus.clam.addr, sizeof(struct sockaddr_in));
	    data->socketAddress.sin_port = htons(port);
	    if (ConnConnectEx(data, NULL, 0, NULL, client->conn->trace.destination) < 0) {
		    ConnFree(conn);
		    ConnFree(data);
		    return -1;
	    }

	    size = 0;
	    if (((ccode = NMAPSendCommandF(client->conn, "QRETR %s MESSAGE\r\n", queueID)) != -1)
		&& ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1)
		&& ccode == 2023) {
		    char *ptr;

		    ptr = strchr (client->line, ' ');
		    if (ptr) {
		        *ptr = '\0';
		    }
		
		    size = atol(client->line);
	    }

	    if (size == 0) {
		    ConnFree(conn);
		    ConnFree(data);
		    return -1;
	    }

	    ccode = ConnReadToConn(client->conn, data, size);
	    
	    ConnFree(data);
	    
	    if ((ccode == -1) || ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != 1000)) {
            XplConsolePrintf("DEBUG: result %d\n", ccode);
		    ConnFree(conn);
		    return -1;
	    }

	    while ((ccode = ConnReadAnswer(conn, client->line, CONN_BUFSIZE)) != -1) {
		char *ptr;

		ptr = strrchr(client->line, ' ');
		if (XplStrCaseCmp(ptr + 1, "FOUND") == 0) {
		    *ptr = '\0';
		    ptr = client->line + strlen("stream: ");

		    if(client->foundViruses.used == client->foundViruses.allocated) {
			client->foundViruses.names = MemRealloc(client->foundViruses.names, sizeof(char*) * (client->foundViruses.allocated + MIME_REALLOC_SIZE));
		    }
		    client->foundViruses.names[client->foundViruses.used++] = MemStrdup(ptr);
		    
		    XplSafeIncrement(AVirus.stats.viruses);
		    infected = 1;
		}
	    }
	}
	ConnFree(conn);
    }

    return infected;
}

static __inline int
ScanMessage(AVClient *client, char *qID)
{
    int ccode = 0;
    if (AVirus.flags & (AV_FLAG_USE_CLAMAV)) {
	ccode = ScanMessageClam(client, qID);
    }
    
    if (ccode != -1 && (AVirus.flags & (AV_FLAG_USE_CA | AV_FLAG_USE_MCAFEE | AV_FLAG_USE_SYMANTEC | AV_FLAG_USE_COMMANDAV))) {
	ccode = ScanMessageParts(client, qID);
    }

    return ccode;
}

static __inline int 
SendNotification(AVClient *client, unsigned char *from, unsigned char *qID, MDBValueStruct *nUsers)
{
    int ccode;
    unsigned long used;
    unsigned char *ptr;
    unsigned char *subject = NULL;

    ccode = -1;
    if (from 
            && (from[0] != '-') 
            && ((ccode = NMAPSendCommandF(client->conn, "QGREP %s subject:\r\n", qID)) != -1)) {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
    } else {
        return(ccode);
    }

    while ((ccode != 1000) && (ccode != -1)) {
        if (ccode == 2002) {
            if (!subject) {
                /* Message infected -> + \0 */
                subject = MemMalloc(strlen(client->line) + 24);
                if (subject) {
                    if (client->line[8]!='\0') {
                        sprintf(subject, "Subject: Virus Alert [%s]", client->line+9);
                    } else {
                        sprintf(subject, "Subject: Virus Alert [%s]", client->line+8);
                    }
                }
            } else if ((ptr = MemRealloc(subject, strlen(subject) + strlen(client->line) + 4)) != NULL) {
                subject = ptr;
                strcat(subject, "\r\n");
                strcat(subject, client->line);
            }
        }

        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPSendCommand(client->conn, "QCREA\r\n", 7)) != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
            && (ccode == 1000)) {
        ccode = NMAPSendCommand(client->conn, "QSTOR FROM - -\r\n", 16);
    } else {
        if (subject) {
            MemFree(subject);
        }

        return(ccode);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
            && (ccode == 1000)) {
        for (used = 0; (used < nUsers->Used) && (ccode != -1); used += 2) {
            if (strchr(nUsers->Value[used], '@')) {
                ccode = NMAPSendCommandF(client->conn, "QSTOR TO %s %s %lu\r\n", nUsers->Value[used], nUsers->Value[used + 1], (long unsigned int)0);
            } else {
                ccode = NMAPSendCommandF(client->conn, "QSTOR LOCAL %s %s %lu\r\n", nUsers->Value[used], nUsers->Value[used+1], (long unsigned int)0);
            }

            if (ccode != -1) {
                ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
            }
        }
    } else {
        if (subject) {
            MemFree(subject);
        }

        NMAPSendCommand(client->conn, "\r\n.\r\nQABRT\r\n", 12);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);

        return(ccode);
    }

/* TODO: AVirus.officialName ???? */
    if ((ccode != -1) 
            && ((ccode = NMAPSendCommand(client->conn, "QSTOR MESSAGE\r\n", 15)) != -1) 
            && ((ccode = ConnWriteF(client->conn, "From: Virus Scanning Agent <postmaster@mailserver>\r\n")) != -1) 
            && ((ccode = ConnWriteF(client->conn, "To: undisclosed-recipient-list\r\n")) != -1)) {
        MsgGetRFC822Date(-1, 0, client->line);
    } else {
        if (subject) {
            MemFree(subject);
        }

        NMAPSendCommand(client->conn, "\r\n.\r\nQABRT\r\n", 12);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);

        return(ccode);
    }

    if (((ccode = ConnWriteF(client->conn, "Date: %s\r\n", client->line)) != -1) 
            && ((ccode = ConnWrite(client->conn, subject, strlen(subject))) != -1) 
            && ((ccode = ConnWrite(client->conn, "\r\nPrecedence: bulk\r\n", 20)) != -1) 
            && ((ccode = ConnWrite(client->conn, "X-Sender: Bongo AntiVirus Agent\r\n", 35)) != -1) 
            && ((ccode = ConnWrite(client->conn, "MIME-Version: 1.0\r\n", 19)) != -1) 
            && ((ccode = ConnWrite(client->conn, "Content-Type: text/plain; charset=\"UTF-8\"\r\n", 43)) != -1) 
            && ((ccode = ConnWrite(client->conn, "Content-Transfer-Encoding: 8bit\r\n\r\n", 35)) != -1) 
            && ((ccode = ConnWriteF(client->conn, "User %s tried to send you a message\r\n", from)) != -1)) {
        for (used = 0; used < client->foundViruses.used; used++) {
            if (client->foundViruses.names[used]) {
                ccode = ConnWriteF(client->conn, "containing the %s virus.\r\n\r\n", client->foundViruses.names[used]);
                break;
            }
        }

        if ((ccode != -1) 
                && (AVirus.flags & AV_FLAG_NOTIFY_SENDER) 
                && ((ccode = ConnWrite(client->conn, "The infected message has been returned to the sender with a notice\r\n", 68)) != -1)) {
            ccode = ConnWrite(client->conn, "that it was infected.\r\n", 23);
        }
    } else {
        if (subject) {
            MemFree(subject);
        }

        NMAPSendCommand(client->conn, "\r\n.\r\nQABRT\r\n", 12);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);

        return(ccode);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPSendCommand(client->conn, "\r\n.\r\nQRUN\r\n", 11)) != -1)) {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
    } else {
        NMAPSendCommand(client->conn, "\r\n.\r\nQABRT\r\n", 12);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
    }

    if (subject) {
        MemFree(subject);
    }

    return(ccode);
}

static __inline int 
ProcessConnection(AVClient *client)
{
    int ccode;
    int length;
    unsigned long source = 0;
    unsigned long used;
    unsigned char preserve;
    unsigned char *ptr;
    unsigned char *cur;
    unsigned char *eol;
    unsigned char *line;
    unsigned char *email;
    unsigned char *from = NULL;
    unsigned char qID[16];
    BOOL copy;
    BOOL infected = FALSE;
    MDBValueStruct *nUsers;

    if (((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
            && (ccode == 6020) 
            && ((ptr = strchr(client->line, ' ')) != NULL)) {
        *ptr++ = '\0';

        strcpy(qID, client->line);

        length = atoi(ptr);
        client->envelope = MemMalloc(length + 3);
    } else {
        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    if (client->envelope) {
        sprintf(client->line, "AVirus: %s", qID);
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
    } else {
        MemFree(client->envelope);
        client->envelope = NULL;
	
        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    if (AVirus.flags & AV_FLAG_NOTIFY_USER) {
	nUsers = MDBShareContext(client->uservs);
	if (!nUsers) {
	    MemFree(client->envelope);
	    client->envelope = NULL;
	    
	    NMAPSendCommand(client->conn, "QDONE\r\n", 7);
	    NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
	    return(-1);
	}
    } else {
	nUsers = NULL;
    }

    preserve = '\0';
    
    if (AVirus.flags & AV_FLAG_SCAN_INCOMING) {
        XplSafeIncrement(AVirus.stats.messages.scanned);

	ccode = ScanMessage(client, qID);
	
	if (ccode == -1) {
	    MemFree(client->envelope);
	    client->envelope = NULL;
	    
	    NMAPSendCommand(client->conn, "QDONE\r\n", 7);
	    NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
	    return(-1);
	}

        infected = (ccode > 0);

        if (infected) {
            XplSafeIncrement(AVirus.stats.attachments.blocked);

            eol = NULL;
            cur = client->envelope;
            while ((ccode != -1) && *cur) {
                copy = TRUE;

                if (eol) {
                    *eol = preserve;
                }

                line = strchr(cur, 0x0A);
                if (line) {
                    if (line[-1] == 0x0D) {
                        eol = line - 1;
                    } else {
                        eol = line;
                    }

                    preserve = *eol;
                    *eol = '\0';

                    line++;
                } else {
                    eol = NULL;
                    line = cur + strlen(cur);
                }

                switch (cur[0]) {
                    case QUEUE_FROM: {
                        if (!from) {
                            ptr = cur + 1;
                            while(*ptr && !isspace(*ptr)) {
                                ++ptr;
                            }

                            *ptr = '\0';
                            from = MemStrdup(cur + 1);
                            *ptr = ' ';
                        }

                        break;
                    }

                    case QUEUE_RECIP_REMOTE:
                    case QUEUE_RECIP_LOCAL:
                    case QUEUE_RECIP_MBOX_LOCAL: {
                        email = strchr(cur + 1, ' ');
                        if (email) {
                            *email++ = '\0';

                            ptr = strchr(email, ' ');
                            if (ptr) {
                                *ptr = '\0';
                            }
                        } else {
                            email = cur + 1;
                            ptr = NULL;
                        }

                        client->line[0] = '\0';

                        if ((AVirus.flags & AV_FLAG_NOTIFY_SENDER)
                                && ((ccode = NMAPSendCommandF(client->conn, "QGREP %s Precedence:\r\n", qID)) != -1)
                                && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1)) {
                            BOOL bounce = TRUE;
                            while ((ccode != 1000) && (ccode != -1)) {
                                if (bounce && (ccode == 2002) && (strlen(client->line) > 12) 
                                        && ((XplStrCaseCmp(client->line + 12, "bulk") == 0)
                                        || (XplStrCaseCmp(client->line + 12, "list") == 0)
                                        || (XplStrCaseCmp(client->line + 12, "junk") == 0))) {
                                    bounce = FALSE;
                                }
                                ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
                            }

                            if (bounce) {
                                for (used = 0; used < client->foundViruses.used; used++) {
                                    if (client->foundViruses.names[used]) {
                                        sprintf(client->line, "Your message is being returned since it seems to contain the %s virus", client->foundViruses.names[used]);
                                        break;
                                    }
                                }

                                ccode = NMAPSendCommandF(client->conn, "QRTS %s %s %lu %d %s\r\n", cur + 1, email, (long unsigned int)(DSN_HEADER | DSN_FAILURE), DELIVER_VIRUS_REJECT, client->line);
                            }
                        }

                        LoggerEvent(AVirus.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_VIRUS_BLOCKED, LOG_INFO, 0, cur + 1, client->foundViruses.used > 0 ? client->foundViruses.names[0] : "", source, 0, from, from? strlen(from) + 1: 0);

                        if (email > cur + 1) {
                            email[-1] = ' ';

                            if (ptr) {
                                *ptr = ' ';
                            }
                        }

                        copy = FALSE;
                        if (AVirus.flags & AV_FLAG_NOTIFY_USER) {
                            MDBAddValue(cur + 1, nUsers);
                            MDBAddValue(email, nUsers);
                        }

                        break;
                    }

                    case 'A': {
                        source = atol(cur + 1);
                        break;
                    }
                }

                if (copy && (ccode != -1)) {
                    ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                }

                cur = line;
            }

            if (eol) {
                *eol = preserve;
            }

            if (AVirus.flags & AV_FLAG_NOTIFY_USER) {
                SendNotification(client, from, qID, nUsers);
            }
        }
    } else {
        eol = NULL;
        cur = client->envelope;
        while ((ccode != -1) && *cur) {
            copy = TRUE;

            if (eol) {
                *eol = preserve;
            }

            line = strchr(cur, 0x0A);
            if (line) {
                if (line[-1] == 0x0D) {
                    eol = line - 1;
                } else {
                    eol = line;
                }

                preserve = *eol;
                *eol = '\0';

                line++;
            } else {
                eol = NULL;
                line = cur + strlen(cur);
            }

            switch (cur[0]) {
                case 'F': {
                    /* from */
                    if (!from) {
                        ptr = cur + 1;
                        while(*ptr && !isspace(*ptr)) {
                            ++ptr;
                        }

                        *ptr = '\0';
                        from = MemStrdup(cur + 1);
                        *ptr = ' ';

                        ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);

                        copy = FALSE;
                    }

                    break;
                }

                case 'M':
                case 'L': {
                    /* Local recipient */
                    if (from) {
#if 0
                        /* Always scan, however; if not, uncomment this section */

                        ptr = line - 1;
                        while ((*ptr != ' ') && (ptr > cur)) {
                            ptr--;
                        }

                        dsnFlags = atol(ptr + 1);
                        if (dsnFlags & NO_VIRUSSCANNING) {
                            break;
                        }
#endif

                        email = strchr(cur + 1, ' ');
                        if (email) {
                            *email++ = '\0';

                            ptr = strchr(email, ' ');
                            if (ptr) {
                                *ptr = '\0';
                            }
                        } else {
                            email = cur + 1;
                            ptr = NULL;
                        }

                        if (!MsgFindObject(cur + 1, client->dn, NULL, NULL, client->uservs)) {
                            MDBFreeValues(client->uservs);

                            if (email > cur + 1) {
                                *email = ' ';

                                if (ptr) {
                                    *ptr = ' ';
                                }
                            }

                            break;
                        }

                        MDBFreeValues(client->uservs);
                    } else {
                        break;
                    }

                    // FIXME
                    // if (MsgGetUserFeature(client->dn, FEATURE_ANTIVIRUS, NULL, NULL)) {
			XplSafeIncrement(AVirus.stats.messages.scanned);

			ccode = ScanMessage(client, qID);
			infected = (ccode == 1);

                        if (infected) {
                            BOOL bounce = TRUE;

                            /* Message is infected, do not copy this recipient on it, since he has virus protection */
                            if (AVirus.flags & AV_FLAG_NOTIFY_USER) {
                                MDBAddValue(cur + 1, nUsers);
                                MDBAddValue(email, nUsers);
                            }

                            if ((AVirus.flags & AV_FLAG_NOTIFY_SENDER)
                                    && ((ccode = NMAPSendCommandF(client->conn, "QGREP %s Precedence:\r\n", qID)) != -1)
                                    && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1)) {
                                while ((ccode != 1000) && (ccode != -1)) {
                                    if (bounce && (ccode == 2002) && (strlen(client->line) > 12)
                                            && ((XplStrCaseCmp(client->line + 12, "bulk") != 0)
                                            || (XplStrCaseCmp(client->line + 12, "list") != 0)
                                            || (XplStrCaseCmp(client->line + 12, "junk") != 0))) {
                                        bounce = FALSE;
                                    }
                                    ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
                                }
                            }

                            if (bounce) {
                                client->line[0] = '\0';
                                for (used = 0; used < client->foundViruses.used; used++) {
                                    if (client->foundViruses.names[used]) {
                                        sprintf(client->line, "Your message is being returned since it seems to contain the %s virus", client->foundViruses.names[used]);
                                        break;
                                    }
                                }

                                ccode = NMAPSendCommandF(client->conn, "QRTS %s %s %lu %d %s\r\n", cur + 1, email, (long unsigned int)(DSN_HEADER | DSN_FAILURE), DELIVER_VIRUS_REJECT, client->line);

                                LoggerEvent(AVirus.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_VIRUS_BLOCKED, LOG_INFO, 0, cur + 1, client->foundViruses.used > 0 ? client->foundViruses.names[used] : "", source, 0, from, from? strlen(from) + 1: 0);
                            } else {
                                for (used = 0; used < client->foundViruses.used; used++) {
				    LoggerEvent(AVirus.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_VIRUS_BLOCKED, LOG_INFO, 0, cur + 1, client->foundViruses.names[used], source, 0, from, from? strlen(from) + 1: 0);
				    break;
                                }
                            }

                            XplSafeIncrement(AVirus.stats.attachments.blocked);
                            copy = FALSE;
                        }
                    // }

                    if ((email - 1) > cur + 1) {
                        *(email - 1) = ' ';

                        if (ptr) {
                            *ptr = ' ';
                        }
                    }

                    break;
                }

                case 'A': {
                    source = atol(cur+1);
                    break;
                }
            }

            if (copy) {
                ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
            }

            cur = line;
        }

        if (eol) {
            *eol = preserve;
        }

        if (infected && (AVirus.flags & AV_FLAG_NOTIFY_USER)) {
            SendNotification(client, from, qID, nUsers);
        }
    }

    if (from) {    
        MemFree(from);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPSendCommand(client->conn, "QDONE\r\n", 7)) != -1)) {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
    }

    if (AVirus.flags & AV_FLAG_NOTIFY_USER) {
        MDBDestroyValueStruct(nUsers);
    }

    MemFree(client->envelope);
    client->envelope = NULL;

    return(0);
}

static void 
HandleConnection(void *param)
{
    int ccode;
    long threadNumber = (long)param;
    time_t sleep = time(NULL);
    time_t wokeup;
    AVClient *client;

    if (((client = AVClientAlloc()) == NULL) 
            || ((client->uservs = MDBCreateValueStruct(AVirus.handle.directory, NULL)) == NULL)) {
        if (client) {
            AVClientFree(client);
        }

        XplConsolePrintf("antivirus: New worker failed to startup; out of memory.\r\n");

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);

        XplSafeDecrement(AVirus.nmap.worker.active);

        return;
    }

    do {
	MDBValueStruct *tempv;
	
        XplRenameThread(XplGetThreadID(), "AVirus Worker");

        XplSafeIncrement(AVirus.nmap.worker.idle);

        XplWaitOnLocalSemaphore(AVirus.nmap.worker.todo);

        XplSafeDecrement(AVirus.nmap.worker.idle);

        wokeup = time(NULL);

        XplWaitOnLocalSemaphore(AVirus.nmap.semaphore);

        client->conn = AVirus.nmap.worker.tail;
        if (client->conn) {
            AVirus.nmap.worker.tail = client->conn->queue.previous;
            if (AVirus.nmap.worker.tail) {
                AVirus.nmap.worker.tail->queue.next = NULL;
            } else {
                AVirus.nmap.worker.head = NULL;
            }
        }

        XplSignalLocalSemaphore(AVirus.nmap.semaphore);

        if (client->conn) {
            if (ConnNegotiate(client->conn, AVirus.nmap.ssl.context)) {
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
        if (threadNumber == XplSafeRead(AVirus.nmap.worker.active)) {
            if ((wokeup - sleep) > AVirus.nmap.sleepTime) {
                break;
            }
        }

        sleep = time(NULL);

        tempv = client->uservs;
	AVClientAllocCB(client, NULL);
	client->uservs = tempv;

    } while (AVirus.state == AV_STATE_RUNNING);

    FreeClientData(client);

    AVClientFree(client);

    XplSafeDecrement(AVirus.nmap.worker.active);

    XplExitThread(TSR_THREAD, 0);

    return;
}

static void 
AntiVirusServer(void *ignored)
{
    int i;
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSafeIncrement(AVirus.server.active);

    XplRenameThread(XplGetThreadID(), "AntiVirus Server");

    AVirus.state = AV_STATE_RUNNING;

    while (AVirus.state < AV_STATE_STOPPING) {
        if (ConnAccept(AVirus.nmap.conn, &conn) != -1) {
            if (AVirus.state < AV_STATE_STOPPING) {
                conn->ssl.enable = FALSE;

                QUEUE_WORK_TO_DO(conn, id, ccode);
                if (!ccode) {
                    XplSignalLocalSemaphore(AVirus.nmap.worker.todo);

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
                if (AVirus.state < AV_STATE_STOPPING) {
                    LoggerEvent(AVirus.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }

            default: {
                if (AVirus.state < AV_STATE_STOPPING) {
                    XplConsolePrintf("antivirus: Exiting after an accept() failure; error %d\r\n", errno);

                    LoggerEvent(AVirus.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                    AVirus.state = AV_STATE_STOPPING;
                }

                break;
            }
        }

        break;
    }

    /* Shutting down */
    AVirus.state = AV_STATE_UNLOADING;

#if VERBOSE
    XplConsolePrintf("antivirus: Shutting down.\r\n");
#endif

    id = XplSetThreadGroupID(AVirus.id.group);

    if (AVirus.nmap.conn) {
        ConnClose(AVirus.nmap.conn, 1);
        AVirus.nmap.conn = NULL;
    }

    if (AVirus.nmap.ssl.enable) {
        AVirus.nmap.ssl.enable = FALSE;

        if (AVirus.nmap.ssl.conn) {
            ConnClose(AVirus.nmap.ssl.conn, 1);
            AVirus.nmap.ssl.conn = NULL;
        }

        if (AVirus.nmap.ssl.context) {
            ConnSSLContextFree(AVirus.nmap.ssl.context);
            AVirus.nmap.ssl.context = NULL;
        }
    }

    ConnCloseAll(1);

    for (i = 0; (XplSafeRead(AVirus.server.active) > 1) && (i < 60); i++) {
        XplDelay(1000);
    }

#if VERBOSE
    XplConsolePrintf("antivirus: Shutting down %d queue threads\r\n", XplSafeRead(AVirus.nmap.worker.active));
#endif

    XplWaitOnLocalSemaphore(AVirus.nmap.semaphore);

    ccode = XplSafeRead(AVirus.nmap.worker.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(AVirus.nmap.worker.todo);
    }

    XplSignalLocalSemaphore(AVirus.nmap.semaphore);

    for (i = 0; XplSafeRead(AVirus.nmap.worker.active) && (i < 60); i++) {
        XplDelay(1000);
    }

    if (XplSafeRead(AVirus.server.active) > 1) {
        XplConsolePrintf("antivirus: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(AVirus.server.active) - 1);
    }

    if (XplSafeRead(AVirus.nmap.worker.active)) {
        XplConsolePrintf("antivirus: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(AVirus.nmap.worker.active));
    }

    LoggerClose(AVirus.handle.logging);
    AVirus.handle.logging = NULL;

    /* shutdown the scanning engine */

    XplCloseLocalSemaphore(AVirus.nmap.worker.todo);
    XplCloseLocalSemaphore(AVirus.nmap.semaphore);
    XplCloseLocalSemaphore(AVirus.id.semaphore);

    XplRWLockDestroy(&AVirus.lock.pattern);

    StreamioShutdown();
    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    MemPrivatePoolFree(AVirus.nmap.pool);

    MemoryManagerClose(MSGSRV_AGENT_ANTIVIRUS);

#if VERBOSE
    XplConsolePrintf("antivirus: Shutdown complete\r\n");
#endif

    XplSignalLocalSemaphore(AVirus.sem.main);
    XplWaitOnLocalSemaphore(AVirus.sem.shutdown);

    XplCloseLocalSemaphore(AVirus.sem.shutdown);
    XplCloseLocalSemaphore(AVirus.sem.main);

    XplSetThreadGroupID(id);

    return;
}

static BongoConfigItem AVirusConfig[] = {
	{ BONGO_JSON_INT, "o:flags/i", &AVirus.flags },
	{ BONGO_JSON_STRING, "o:patterns/s", &AVirus.path.patterns },
	{ BONGO_JSON_INT, "o:queue/i", &AVirus.nmap.queue },
	{ BONGO_JSON_STRING, "o:host/s", &AVirus.clam.host },
	{ BONGO_JSON_INT, "o:port/i", &AVirus.clam.addr.sin_port },
	{ BONGO_JSON_NULL, NULL, NULL }
};

static BOOL 
ReadConfiguration(void)
{
    unsigned char path[XPL_MAX_PATH + 1];
    XplDir *dir;
    XplDir *dirEntry;
    struct hostent *he;

    if (! ReadBongoConfiguration(AVirusConfig, "antivirus"))
	return FALSE;

    AVirus.clam.addr.sin_family = AF_INET;

    he = gethostbyname(AVirus.clam.host);
    if (he) {
        memcpy(&AVirus.clam.addr.sin_addr.s_addr, he->h_addr_list[0], 
            sizeof(AVirus.clam.addr.sin_addr.s_addr));
    }

    MsgGetWorkDir(AVirus.path.work);

    strcat(AVirus.path.work, "/avirus");
    MsgMakePath(AVirus.path.work);

    dirEntry = XplOpenDir(AVirus.path.work);
    if (dirEntry) {
        while((dir = XplReadDir(dirEntry)) != NULL) {
            sprintf(path, "%s/%s", AVirus.path.work, dir->d_name);
            if (dir->d_nameDOS[0] != '.') {
                unlink(path);
            }
        }

        XplCloseDir(dirEntry);
    }
    return TRUE;
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = TRUE;
        AVirus.state = AV_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(AVirus.sem.shutdown);

        id = XplSetThreadGroupID(AVirus.id.group);
        ConnClose(AVirus.nmap.conn, 1);
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(AVirus.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (AVirus.state < AV_STATE_UNLOADING) {
                AVirus.state = AV_STATE_UNLOADING;
            }

            break;
        }
        case SIGINT:
        case SIGTERM: {
            if (AVirus.state == AV_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (AVirus.state < AV_STATE_STOPPING) {
                AVirus.state = AV_STATE_STOPPING;
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
    AVirus.nmap.conn = ConnAlloc(FALSE);
    if (AVirus.nmap.conn) {
        memset(&(AVirus.nmap.conn->socketAddress), 0, sizeof(AVirus.nmap.conn->socketAddress));

        AVirus.nmap.conn->socketAddress.sin_family = AF_INET;
        AVirus.nmap.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

        /* Get root privs back for the bind.  It's ok if this fails -
        * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);

        AVirus.nmap.conn->socket = ConnServerSocket(AVirus.nmap.conn, 2048);
        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("antivirus: Could not drop to unprivileged user '%s'\r\n", MsgGetUnprivilegedUser());
            ConnFree(AVirus.nmap.conn);
            AVirus.nmap.conn = NULL;
            return(-1);
        }

        if (AVirus.nmap.conn->socket == -1) {
            XplConsolePrintf("antivirus: Could not bind to dynamic port\r\n");
            ConnFree(AVirus.nmap.conn);
            AVirus.nmap.conn = NULL;
            return(-1);
        }

        if (NMAPRegister(MSGSRV_AGENT_ANTIVIRUS, AVirus.nmap.queue, AVirus.nmap.conn->socketAddress.sin_port) != REGISTRATION_COMPLETED) {
            XplConsolePrintf("antivirus: Could not register with bongonmap\r\n");
            ConnFree(AVirus.nmap.conn);
            AVirus.nmap.conn = NULL;
            return(-1);
        }
    } else {
        XplConsolePrintf("antivirus: Could not allocate connection.\r\n");
        return(-1);
    }

    return(0);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int                ccode;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("antivirus: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
        return(1);
    }
    XplInit();

    XplSignalHandler(SignalHandler);

    AVirus.id.main = XplGetThreadID();
    AVirus.id.group = XplGetThreadGroupID();
    AVirus.id.next = 0;

    AVirus.state = AV_STATE_INITIALIZING;
    AVirus.flags = 0;

    AVirus.nmap.conn = NULL;
    AVirus.nmap.queue = Q_INCOMING;
    AVirus.nmap.pool = NULL;
    AVirus.nmap.sleepTime = (5 * 60);
    AVirus.nmap.ssl.conn = NULL;
    AVirus.nmap.ssl.enable = FALSE;
    AVirus.nmap.ssl.context = NULL;
    AVirus.nmap.ssl.config.options = 0;

    AVirus.handle.directory = NULL;
    AVirus.handle.logging = NULL;

    strcpy(AVirus.nmap.address, "127.0.0.1");

    XplSafeWrite(AVirus.server.active, 0);

    XplSafeWrite(AVirus.nmap.worker.idle, 0);
    XplSafeWrite(AVirus.nmap.worker.active, 0);
    XplSafeWrite(AVirus.nmap.worker.maximum, 100000);

    XplSafeWrite(AVirus.stats.messages.scanned, 0);
    XplSafeWrite(AVirus.stats.attachments.scanned, 0);
    XplSafeWrite(AVirus.stats.attachments.blocked, 0);
    XplSafeWrite(AVirus.stats.viruses, 0);

    if (MemoryManagerOpen(MSGSRV_AGENT_ANTIVIRUS) == TRUE) {
        AVirus.nmap.pool = MemPrivatePoolAlloc("Anti-Virus Connections", sizeof(AVClient), 0, 3072, TRUE, FALSE, AVClientAllocCB, NULL, NULL);
        if (AVirus.nmap.pool != NULL) {
            XplOpenLocalSemaphore(AVirus.sem.main, 0);
            XplOpenLocalSemaphore(AVirus.sem.shutdown, 1);
            XplOpenLocalSemaphore(AVirus.id.semaphore, 1);
            XplOpenLocalSemaphore(AVirus.nmap.semaphore, 1);
            XplOpenLocalSemaphore(AVirus.nmap.worker.todo, 1);
        } else {
            MemoryManagerClose(MSGSRV_AGENT_ANTIVIRUS);

            XplConsolePrintf("antivirus: Unable to create connection pool; shutting down.\r\n");
            return(-1);
        }
    } else {
        XplConsolePrintf("antivirus: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    ConnStartup(CONNECTION_TIMEOUT, TRUE);

    MDBInit();
    AVirus.handle.directory = (MDBHandle)MsgInit();
    if (AVirus.handle.directory == NULL) {
        XplBell();
        XplConsolePrintf("antivirus: Invalid directory credentials; exiting!\r\n");
        XplBell();

        MemoryManagerClose(MSGSRV_AGENT_ANTIVIRUS);

        return(-1);
    }

    StreamioInit();
    NMAPInitialize(AVirus.handle.directory);

    XplRWLockInit(&AVirus.lock.pattern);

    SetCurrentNameSpace(NWOS2_NAME_SPACE);
    SetTargetNameSpace(NWOS2_NAME_SPACE);

    AVirus.handle.logging = LoggerOpen("bongoavirus");
    if (!AVirus.handle.logging) {
        XplConsolePrintf("antivirus: Unable to initialize logging; disabled.\r\n");
    }

    ReadConfiguration();
    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "avirus");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    if (QueueSocketInit() < 0) {
        XplConsolePrintf("antivirus: Exiting.\r\n");

        MemoryManagerClose(MSGSRV_AGENT_ANTIVIRUS);

        return -1;
    }

    /* initialize scanning engine here */


    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("antivirus: Could not drop to unprivileged user '%s', exiting.\r\n", MsgGetUnprivilegedUser());

        MemoryManagerClose(MSGSRV_AGENT_ANTIVIRUS);

        return 1;
    }

    AVirus.nmap.ssl.enable = FALSE;
    AVirus.nmap.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
    AVirus.nmap.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
    AVirus.nmap.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

    AVirus.nmap.ssl.context = ConnSSLContextAlloc(&(AVirus.nmap.ssl.config));
    if (AVirus.nmap.ssl.context) {
        AVirus.nmap.ssl.enable = TRUE;
    }

    NMAPSetEncryption(AVirus.nmap.ssl.context);

    XplStartMainThread(PRODUCT_SHORT_NAME, &id, AntiVirusServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    return(0);
}
