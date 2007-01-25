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
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>

#include "pluspack.h"

#if defined(RELEASE_BUILD)
#define PlusPackClientAlloc() MemPrivatePoolGetEntry(PlusPack.nmap.pool)
#else
#define PlusPackClientAlloc() MemPrivatePoolGetEntryDebug(PlusPack.nmap.pool, __FILE__, __LINE__)
#endif

static void SignalHandler(int sigtype);

#define QUEUE_WORK_TO_DO(c, id, r) \
        { \
            XplWaitOnLocalSemaphore(PlusPack.nmap.semaphore); \
            if (XplSafeRead(PlusPack.nmap.queue.idle)) { \
                (c)->queue.previous = NULL; \
                if (((c)->queue.next = PlusPack.nmap.queue.head) != NULL) { \
                    (c)->queue.next->queue.previous = (c); \
                } else { \
                    PlusPack.nmap.queue.tail = (c); \
                } \
                PlusPack.nmap.queue.head = (c); \
                (r) = 0; \
            } else { \
                XplSafeIncrement(PlusPack.nmap.queue.active); \
                XplSignalBlock(); \
                XplBeginThread(&(id), HandleConnection, 24 * 1024, XPL_INT_TO_PTR(XplSafeRead(PlusPack.nmap.queue.active)), (r)); \
                XplSignalHandler(SignalHandler); \
                if (!(r)) { \
                    (c)->queue.previous = NULL; \
                    if (((c)->queue.next = PlusPack.nmap.queue.head) != NULL) { \
                        (c)->queue.next->queue.previous = (c); \
                    } else { \
                        PlusPack.nmap.queue.tail = (c); \
                    } \
                    PlusPack.nmap.queue.head = (c); \
                } else { \
                    XplSafeDecrement(PlusPack.nmap.queue.active); \
                    (r) = -1; \
                } \
            } \
            XplSignalLocalSemaphore(PlusPack.nmap.semaphore); \
        }

PlusPackGlobals PlusPack;

static BOOL 
PlusPackClientAllocCB(void *buffer, void *data)
{
    register PlusPackClient *c = (PlusPackClient *)buffer;

    memset(c, 0, sizeof(PlusPackClient));

    return(TRUE);
}

static void 
PlusPackClientFree(PlusPackClient *client)
{
    register PlusPackClient *c = client;

    if (c->conn) {
        ConnClose(c->conn, 1);
        ConnFree(c->conn);
        c->conn = NULL;
    }

    MemPrivatePoolReturnEntry(c);

    return;
}

static void 
FreeClientData(PlusPackClient *client)
{
    if (client && !(client->flags & PLUSPACK_CLIENT_FLAG_EXITING)) {
        client->flags |= PLUSPACK_CLIENT_FLAG_EXITING;

        if (client->conn) {
            ConnClose(client->conn, 1);
            ConnFree(client->conn);
            client->conn = NULL;
        }

        if (client->vs) {
            MDBDestroyValueStruct(client->vs);
            client->vs = NULL;
        }

        if (client->envelope.data) {
            MemFree(client->envelope.data);
            client->envelope.data = NULL;
            client->envelope.length = 0;
        }
    }

    return;
}

static int 
ProcessConnection(PlusPackClient *client)
{
    int ccode;
    unsigned char preserve;
    unsigned char *cur;
    unsigned char *ptr;
    unsigned char *eol;
    unsigned char *line;
    MDBValueStruct *vs;

    preserve = '\0';
    
    if (((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
            && (ccode == 6020) 
            && ((ptr = strchr(client->line, ' ')) != NULL) 
            && (client->line[3] == '-')) {
        *ptr++ = '\0';

        strcpy(client->entry.name, client->line);

        client->line[3] = '\0';
        client->entry.queue = atol(client->line);
        client->entry.id = atol(client->line + 4);

        client->envelope.length = atoi(ptr);
        client->envelope.data = MemMalloc(client->envelope.length + 3);
        vs = MDBCreateValueStruct(PlusPack.handle.directory, NULL);
    } else {
        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    if (client->envelope.data && vs) {
        sprintf(client->line, "PlusPack: %s", client->entry.name);
        XplRenameThread(XplGetThreadID(), client->line);

        ccode = NMAPReadCount(client->conn, client->envelope.data, client->envelope.length);
    } else {
        if (client->envelope.data) {
            MemFree(client->envelope.data);
        }

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 6021)) {
        client->envelope.data[client->envelope.length] = '\0';

        cur = client->envelope.data;
    } else {
        MemFree(client->envelope.data);
        client->envelope.data = NULL;

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    eol = NULL;
    while (*cur) {
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
            case QUEUE_CALENDAR_LOCAL: {
                client->recipients.calendar++;
                break;
            }

            case QUEUE_FROM: {
                unsigned char *auth;

                auth = strchr(cur + 1, ' ');
                if (auth) {
                    *auth = '\0';
                    strcpy(client->sender.claim, cur + 1);
                    *auth++ = ' ';

                    ptr = strchr(auth, ' ');
                    if (ptr) {
                        *ptr = '\0';
                        strcpy(client->sender.auth, auth);
                        *ptr = ' ';
                    } else {
                        strcpy(client->sender.auth, auth);
                    }
                } else {
                    strcpy(client->sender.claim, cur + 1);

                    client->sender.auth[0] = '-';
                    client->sender.auth[0] = '\0';
                }

                break;
            }

            case QUEUE_RECIP_LOCAL: 
            case QUEUE_RECIP_MBOX_LOCAL: {
                client->recipients.local++;
                break;
            }

            case QUEUE_RECIP_REMOTE: {
                client->recipients.remote++;
                break;
            }

            case QUEUE_FLAGS: {
                client->entry.flags = atol(cur + 1);
                break;
            }

            case QUEUE_ADDRESS: 
            case QUEUE_BOUNCE: 
            case QUEUE_DATE: 
            case QUEUE_ID: 
            case QUEUE_THIRD_PARTY: 
            default: {
                break;
            }
        }

        cur = line;
    }

    if (eol) {
        *eol = preserve;
    }

    return(ccode);
}

static void 
ProcessACL(PlusPackClient *client)
{
    unsigned long used;
    unsigned char *ptr;
    unsigned char *delim;

    client->dn[0] = '\0';
    client->flags &= ~(PLUSPACK_CLIENT_FLAG_FOUND | PLUSPACK_CLIENT_FLAG_ALLOWED);

    if ((client->sender.auth[0] != '-') && (client->sender.auth[0] != '\0')) {
        ptr = client->sender.auth;
    } else if (!(PlusPack.flags & PLUSPACK_FLAG_ACL_AUTH_REQUIRED)) {
        ptr = client->sender.claim;
    } else {
        return;
    }

    delim = strchr(ptr, '@');
    if (delim) {
        *delim = '\0';
    }

    if (MsgFindObject(ptr, client->dn, NULL, NULL, client->vs)) {
        client->flags |= PLUSPACK_CLIENT_FLAG_FOUND;

        if (delim) {
            *delim = '@';
        }
    } else if (delim) {
        *delim = '@';

        delim = strchr(ptr, ' ');
        if (delim) {
            *delim = '\0';
        }

        if (MsgFindObject(ptr, client->dn, NULL, NULL, client->vs)) {
            client->flags |= PLUSPACK_CLIENT_FLAG_FOUND;
        }

        if (delim) {
            *delim = ' ';
        }
    }

    if (client->dn[0]) {
        MDBFreeValues(client->vs);
        MDBReadDN(client->dn, A_GROUP_MEMBERSHIP, client->vs);
        for (used = 0; used < client->vs->Used; used++) {
            if (XplStrCaseCmp(PlusPack.allowedGroup, client->vs->Value[used]) != 0) {
                continue;
            }

            client->flags |= PLUSPACK_CLIENT_FLAG_ALLOWED;
            break;
        }
    }

    if (client->flags & PLUSPACK_CLIENT_FLAG_FOUND) {
        if (client->flags & PLUSPACK_CLIENT_FLAG_ALLOWED) {
            if (PlusPack.flags & PLUSPACK_FLAG_ACL_SENDNOMEMBERONLY) {
                client->flags &= ~PLUSPACK_CLIENT_FLAG_ALLOWED;
            }
        } else if (PlusPack.flags & PLUSPACK_FLAG_ACL_SENDNOMEMBERONLY) {
            client->flags |= PLUSPACK_CLIENT_FLAG_ALLOWED;
        }
    }

    MDBFreeValues(client->vs);

    return;
}

static int 
ProcessCopy(PlusPackClient *client)
{
    int count;
    int length;
    unsigned char *cur;
    unsigned char *line;
    unsigned char *envelope;

    if (!(client->entry.flags & MSG_FLAG_PLUSPACK_COPIED) 
            && (((client->entry.queue == Q_FIVE) && (client->recipients.local)) 
                || ((client->entry.queue == Q_DELIVER) && (client->recipients.remote)))) {
        count = client->envelope.length + 39;
        count += (strlen(PlusPack.copy.user) * 2);
        count += strlen(PlusPack.copy.inbox);

        envelope = (unsigned char *)MemMalloc(count);
        if (envelope) {
            client->entry.flags |= MSG_FLAG_PLUSPACK_COPIED;

            count = 0;
            length = client->envelope.length;
            cur = client->envelope.data;
            while (*cur) {
                line = strchr(cur, 0x0A);
                if (line) {
                    line++;
                } else {
                    line = cur + strlen(cur);
                }

                if (cur[0] != QUEUE_FLAGS) {
                    cur = line;
                    continue;
                }

                count = cur - client->envelope.data;
                memcpy(envelope, client->envelope.data, count);

                count += sprintf(envelope + count, QUEUES_FLAGS"%lu\r\n", client->entry.flags);

                length -= (line - client->envelope.data);

                memcpy(envelope + count, line, length);

                count += length;
                break;
            }

            if (!count) {
                count = sprintf(envelope, QUEUES_FLAGS"%lu\r\n", client->entry.flags);
                memcpy(envelope + count, client->envelope.data, client->envelope.length);
                count += client->envelope.length;
            }

            MemFree(client->envelope.data);
            client->envelope.data = envelope;
            client->envelope.length = count;
            envelope[count] = '\0';

            if (client->entry.queue == Q_FIVE) {
                if (client->recipients.local) {
                    client->flags |= PLUSPACK_CLIENT_FLAG_COPIED;
                    client->recipients.local++;

                    if (PlusPack.copy.inbox[0]) {
                        count = sprintf(envelope + client->envelope.length, QUEUES_RECIP_MBOX_LOCAL"%s %s 0 %s %d\r\n", PlusPack.copy.user,  PlusPack.copy.user, PlusPack.copy.inbox, NO_PLUSPACK);
                    } else {
                        count = sprintf(envelope + client->envelope.length, QUEUES_RECIP_LOCAL"%s %s %d\r\n", PlusPack.copy.user,  PlusPack.copy.user, NO_PLUSPACK);
                    }

                    client->envelope.length += count;
                }
            } else if (client->recipients.remote) {
                client->flags |= PLUSPACK_CLIENT_FLAG_COPIED;
                client->recipients.local++;

                if (PlusPack.copy.outbox[0]) {
                    count = sprintf(envelope + client->envelope.length, QUEUES_RECIP_MBOX_LOCAL"%s %s 0 %s %d\r\n", PlusPack.copy.user,  PlusPack.copy.user, PlusPack.copy.outbox, NO_PLUSPACK);
                } else {
                    count = sprintf(envelope + client->envelope.length, QUEUES_RECIP_LOCAL"%s %s %d\r\n", PlusPack.copy.user,  PlusPack.copy.user, NO_PLUSPACK);
                }

                client->envelope.length += count;
            }
        } else {
            LoggerEvent(PlusPack.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(MDBValueStruct), __LINE__, NULL, 0);

            count = 0;
        }
    } else {
        count = 0;
    }

    return(count);
}

static int 
ProcessMime(PlusPackClient *client)
{
    int ccode;
    int count;
    unsigned long size;
    unsigned long position;
    unsigned char *ptr;
    unsigned char *type = NULL;
    unsigned char *subtype = NULL;
    unsigned char *charset = NULL;
    unsigned char *encoding = NULL;

    count = 0;

    ccode = NMAPSendCommandF(client->conn, "QMIME %s\r\n", client->entry.name);
    while ((ccode != -1) && (ccode != NMAP_OK)) {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
        switch (ccode) {
            case NMAP_OK: {
                continue;
            }

            case 2002: {
                ptr = NULL;

                type = client->line;
                subtype = strchr(client->line, ' ');
                if (subtype) {
                    *subtype++ = '\0';

                    charset = strchr(subtype, ' ');
                    if (charset) {
                        *charset++ = '\0';

                        encoding = strchr(charset, ' ');
                        if (encoding) {
                            *encoding++ = '\0';

                            ptr = strchr(encoding, ' ');
                            if (ptr) {
                                *ptr++ = '\0';

                                ptr = strrchr(ptr, '"');
                                if (ptr) {
                                    ptr += 2;
                                }
                            }
                        }
                    }
                }

                if (type && subtype && charset && encoding && ptr) {
                    if (sscanf(ptr, "%lu %lu", &position, &size) == 2) {
                        if (!count) {
                            if (XplStrCaseCmp(type, "multipart") == 0) {
                                if (XplStrCaseCmp(subtype, "alternative") == 0) {
                                    count++;
                                } else if (XplStrCaseCmp(subtype, "mixed") == 0) {
                                    count++;

                                    client->mime.attachments = TRUE;
                                }
                            }
                        }
                        
                        if (count < 2) {
                            if (XplStrCaseCmp(type, "text") == 0) {
                                if (XplStrCaseCmp(subtype, "plain") == 0) {
                                    client->flags |= PLUSPACK_CLIENT_FLAG_SIGNATURE;

                                    client->mime.plain.position = position;
                                    client->mime.plain.size = size;
                                } else if (XplStrCaseCmp(subtype, "html") == 0) {
                                    client->flags |= PLUSPACK_CLIENT_FLAG_SIGNATURE_HTML;

                                    client->mime.html.position = position;
                                    client->mime.html.size = size;
                                } else {
                                    client->mime.attachments = TRUE;
                                }
                            } else if ((XplStrCaseCmp(type, "multipart") != 0) 
                                    && (XplStrCaseCmp(type, "message") != 0)) {
                                client->mime.attachments = TRUE;
                            }
                        }
                    }
                }

                continue;
            }

            case 2003: 
            case 2004: {
                count--;
                continue;
            }

            default: {
                ccode = -1;
                continue;
            }
        }
    }

    if ((client->flags & PLUSPACK_CLIENT_FLAG_SIGNATURE) 
            && (client->flags & PLUSPACK_CLIENT_FLAG_SIGNATURE_HTML)) {
        if (client->mime.html.position < client->mime.plain.position) {
            client->mime.signature[0].html = TRUE;
            client->mime.signature[0].position = client->mime.html.position;
            client->mime.signature[0].size = client->mime.html.size;
            client->mime.signature[1].html = FALSE;
            client->mime.signature[1].position = client->mime.plain.position;
            client->mime.signature[1].size = client->mime.plain.size;
        } else {
            client->mime.signature[0].html = FALSE;
            client->mime.signature[0].position = client->mime.plain.position;
            client->mime.signature[0].size = client->mime.plain.size;
            client->mime.signature[1].html = TRUE;
            client->mime.signature[1].position = client->mime.html.position;
            client->mime.signature[1].size = client->mime.html.size;
        }
    } else if (client->flags & PLUSPACK_CLIENT_FLAG_SIGNATURE) {
        client->mime.signature[0].html = FALSE;
        client->mime.signature[0].position = client->mime.plain.position;
        client->mime.signature[0].size = client->mime.plain.size;
        client->mime.signature[1].position = 0;
    } else if (client->flags & PLUSPACK_CLIENT_FLAG_SIGNATURE_HTML) {
        client->mime.signature[0].html = TRUE;
        client->mime.signature[0].position = client->mime.html.position;
        client->mime.signature[0].size = client->mime.html.size;
        client->mime.signature[1].position = 0;
    }

    if (((ccode = NMAPSendCommandF(client->conn, "QINFO %s\r\n", client->entry.name)) != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 2001) 
            && (sscanf(client->line, "%*s %lu %lu", &client->mime.size, &client->mime.header) == 2)) {
        return(0);
    }

    return(-1);
}

static int 
ProcessAttachments(PlusPackClient *client)
{
    int ccode = 0;
    unsigned char preserve;
    unsigned char *cur;
    unsigned char *eol;
    unsigned char *line;

    preserve = '\0';
    if ((client->entry.queue == Q_DELIVER) && client->mime.attachments) {
        client->flags &= ~(PLUSPACK_FLAG_SIGNATURE_HTML | PLUSPACK_FLAG_SIGNATURE);

        eol = NULL;
        cur = client->envelope.data;
        while (*cur) {
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
                case QUEUE_CALENDAR_LOCAL: 
                case QUEUE_RECIP_LOCAL: 
                case QUEUE_RECIP_MBOX_LOCAL: 
                case QUEUE_RECIP_REMOTE: {
                    /*  If the COPIED bit has been set; then the last recipient in the envelope
                        is the copied user and we need to preserve it.

                        Otherwise; consume all recipients.
                    */
                    if (!(client->flags & PLUSPACK_CLIENT_FLAG_COPIED) || *line) {
                        break;
                    }

                    ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                    break;
                }

                case QUEUE_FLAGS: {
                    ccode = NMAPSendCommandF(client->conn, "QMOD RAW "QUEUES_FLAGS"%d\r\n", client->entry.flags | MSG_FLAG_PLUSPACK_PROCESSED);
                    break;
                }

                case QUEUE_ADDRESS: 
                case QUEUE_BOUNCE: 
                case QUEUE_DATE: 
                case QUEUE_FROM: 
                case QUEUE_ID: 
                case QUEUE_THIRD_PARTY: 
                default: {
                    ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                    break;
                }
            }

            cur = line;
        }

        if (eol) {
            *eol = preserve;
        }

        client->bounce = "You are not allowed to send attatchments.";
    }

    return(ccode);
}

static int 
ProcessAllowed(PlusPackClient *client)
{
    int ccode = 0;
    unsigned char preserve;
    unsigned char *cur;
    unsigned char *eol;
    unsigned char *line;

    preserve = '\0';
    if ((client->entry.queue == Q_DELIVER) && client->recipients.remote && !(client->flags & PLUSPACK_CLIENT_FLAG_ALLOWED)) {
        client->flags &= ~(PLUSPACK_FLAG_SIGNATURE_HTML | PLUSPACK_FLAG_SIGNATURE);

        if (!client->bounce) {
            eol = NULL;
            cur = client->envelope.data;
            while (*cur) {
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
                    case QUEUE_CALENDAR_LOCAL: 
                    case QUEUE_RECIP_LOCAL: 
                    case QUEUE_RECIP_MBOX_LOCAL: {
                        ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                        break;
                    }

                    case QUEUE_RECIP_REMOTE: {
                        break;
                    }

                    case QUEUE_ADDRESS: 
                    case QUEUE_BOUNCE: 
                    case QUEUE_DATE: 
                    case QUEUE_FROM: 
                    case QUEUE_ID: 
                    case QUEUE_THIRD_PARTY: 
                    case QUEUE_FLAGS: 
                    default: {
                        ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                        break;
                    }
                }

                cur = line;
            }

            if (eol) {
                *eol = preserve;
            }

            client->bounce = "You are not allowed to send external mail.";
        } else {
            client->bounce = "You are not allowed to send external mail; or attachments.";
        }
    }

    return(ccode);
}

static int 
ProcessEnvelope(PlusPackClient *client)
{
    int ccode = 0;
    unsigned char preserve;
    unsigned char *cur;
    unsigned char *eol;
    unsigned char *line;
    
    preserve = '\0';
    if (client->flags & PLUSPACK_CLIENT_FLAG_COPIED) {
        eol = NULL;
        cur = client->envelope.data;
        while ((ccode != -1) && *cur) {
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

            ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);

            cur = line;
        }

        if (eol) {
            *eol = preserve;
        }
    }

    return(ccode);
}

static int 
ProcessSignatures(PlusPackClient *client)
{
    int ccode;
    int count;
    unsigned char preserve;
    unsigned char *ptr;
    unsigned char *cur;
    unsigned char *eol;
    unsigned char *line;

    preserve = '\0';
    if ((client->entry.queue == Q_DELIVER) && client->recipients.remote) {
        ccode = NMAPSendCommand(client->conn, "QCREA\r\n", 7);
    } else {
        ccode = ProcessEnvelope(client);
        return(ccode);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
        client->flags |= PLUSPACK_CLIENT_FLAG_SPLIT;

        if (((ccode = NMAPSendCommandF(client->conn, "QSTOR FLAGS %d\r\n", client->entry.flags | MSG_FLAG_PLUSPACK_PROCESSED)) != -1) 
                && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                && ((ccode = NMAPSendCommandF(client->conn, "QSTOR FROM %s %s\r\n", client->sender.claim, client->sender.auth)) != -1) 
                && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                && ((ccode = NMAPSendCommandF(client->conn, "QADDQ %s 0 %lu\r\n", client->entry.name, client->mime.header)) != -1) 
                && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                && ((ccode = NMAPSendCommandF(client->conn, "QSTOR MESSAGE\r\nX-AutoSignature-Version: Bongo Signature Agent %s\r\n.\r\n", GetPlusPackVersion())) != -1) 
                && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
            if ((client->flags & PLUSPACK_CLIENT_FLAG_SIGNATURE) 
                    && (client->flags & PLUSPACK_CLIENT_FLAG_SIGNATURE_HTML)) {
                if (((ccode = NMAPSendCommandF(client->conn, "QADDQ %s %lu %lu\r\n", client->entry.name, client->mime.header, client->mime.signature[0].position + client->mime.signature[0].size)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
                    if (client->mime.signature[0].html) {
                        ptr = PlusPack.signature.html;
                    } else {
                        ptr = PlusPack.signature.plain;
                    }

                    count = strlen(ptr);
                    if ((ccode = NMAPSendCommandF(client->conn, "QSTOR MESSAGE %d\r\n", count)) != -1) {
                        ccode = NMAPSendCommand(client->conn, ptr, count);
                    }
                } else {
                    ccode = -1;
                }

                if ((ccode != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                        && ((ccode = NMAPSendCommand(client->conn, "QSTOR MESSAGE 2\r\n\r\n", 19)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                        && ((ccode = NMAPSendCommandF(client->conn, "QADDQ %s %lu %lu\r\n", client->entry.name, client->mime.header + client->mime.signature[0].position + client->mime.signature[0].size, (client->mime.signature[1].position - client->mime.signature[0].position - client->mime.signature[0].size) + client->mime.signature[1].size)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
                    if (client->mime.signature[1].html) {
                        ptr = PlusPack.signature.html;
                    } else {
                        ptr = PlusPack.signature.plain;
                    }

                    count = strlen(ptr);
                    if ((ccode = NMAPSendCommandF(client->conn, "QSTOR MESSAGE %d\r\n", count)) != -1) {
                        ccode = NMAPSendCommand(client->conn, ptr, count);
                    }
                } else {
                    ccode = -1;
                }

                if ((ccode != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                        && ((ccode = NMAPSendCommand(client->conn, "QSTOR MESSAGE 2\r\n\r\n", 19)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
                    ccode = NMAPSendCommandF(client->conn, "QADDQ %s %lu %lu\r\n", client->entry.name, client->mime.header + client->mime.signature[1].position + client->mime.signature[1].size, client->mime.size - client->mime.header - client->mime.signature[1].position - client->mime.signature[1].size);
                } else {
                    ccode = -1;
                }
            } else if (client->flags & (PLUSPACK_CLIENT_FLAG_SIGNATURE | PLUSPACK_CLIENT_FLAG_SIGNATURE_HTML)) {
                if (((ccode = NMAPSendCommandF(client->conn, "QADDQ %s %lu %lu\r\n", client->entry.name, client->mime.header, client->mime.signature[0].position + client->mime.signature[0].size)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
                    if (client->mime.signature[0].html) {
                        ptr = PlusPack.signature.html;
                    } else {
                        ptr = PlusPack.signature.plain;
                    }

                    count = strlen(ptr);
                    if ((ccode = NMAPSendCommandF(client->conn, "QSTOR MESSAGE %d\r\n", count)) != -1) {
                        ccode = NMAPSendCommand(client->conn, ptr, count);
                    }
                } else {
                    ccode = -1;
                }

                if ((ccode != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK) 
                        && ((ccode = NMAPSendCommand(client->conn, "QSTOR MESSAGE 2\r\n\r\n", 19)) != -1) 
                        && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == NMAP_OK)) {
                    ccode = NMAPSendCommandF(client->conn, "QADDQ %s %lu %lu\r\n", client->entry.name, client->mime.header + client->mime.signature[0].position + client->mime.signature[0].size, client->mime.size - client->mime.header - client->mime.signature[0].position - client->mime.signature[0].size);
                } else {
                    ccode = -1;
                }
            } else {
                ccode = NMAPSendCommandF(client->conn, "QADDQ %s %lu %lu\r\n", client->entry.name, client->mime.header, client->mime.size - client->mime.header);
            }

            if (ccode != -1) {
                ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);

                eol = NULL;
                cur = client->envelope.data;
                while ((ccode != -1) && *cur) {
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
                        case QUEUE_CALENDAR_LOCAL: 
                        case QUEUE_RECIP_LOCAL: 
                        case QUEUE_RECIP_MBOX_LOCAL: {
                            ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                            break;
                        }

                        case QUEUE_RECIP_REMOTE: {
                            if ((client->flags & PLUSPACK_CLIENT_FLAG_ALLOWED) 
                                    && ((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW %s\r\n", cur)) != -1)) {
                                ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
                            }

                            break;
                        }

                        case QUEUE_FROM: 
                        case QUEUE_DATE: 
                        case QUEUE_FLAGS: {
                            ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
                            break;
                        }

                        case QUEUE_ADDRESS: 
                        case QUEUE_BOUNCE: 
                        case QUEUE_ID: 
                        case QUEUE_THIRD_PARTY: 
                        default: {
                            if (((ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur)) != -1) 
                                    && ((ccode = NMAPSendCommandF(client->conn, "QSTOR RAW %s\r\n", cur)) != -1)) {
                                ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE);
                            }

                            break;
                        }
                    }

                    cur = line;
                }

                if (eol) {
                    *eol = preserve;
                }
            }
        }

        if (ccode == -1) {
            client->flags &= ~PLUSPACK_CLIENT_FLAG_SPLIT;

            NMAPSendCommand(client->conn, "QABRT\r\n", 7);
            NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        }
    } else {
        ccode = -1;
    }

    return(ccode);
}

static void 
HandleConnection(void *param)
{
    int ccode;
    long threadNumber = (long)param;
    time_t sleep = time(NULL);
    time_t wokeup;
    PlusPackClient *client;

    if ((client = PlusPackClientAlloc()) == NULL) {
        XplConsolePrintf("bongopluspack: New worker failed to startup; out of memory.\r\n");

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);

        XplSafeDecrement(PlusPack.nmap.queue.active);

        return;
    }

    do {
        XplRenameThread(XplGetThreadID(), "PlusPack Worker");

        XplSafeIncrement(PlusPack.nmap.queue.idle);

        XplWaitOnLocalSemaphore(PlusPack.nmap.queue.todo);

        XplSafeDecrement(PlusPack.nmap.queue.idle);

        wokeup = time(NULL);

        XplWaitOnLocalSemaphore(PlusPack.nmap.semaphore);

        client->conn = PlusPack.nmap.queue.tail;
        if (client->conn) {
            PlusPack.nmap.queue.tail = client->conn->queue.previous;
            if (PlusPack.nmap.queue.tail) {
                PlusPack.nmap.queue.tail->queue.next = NULL;
            } else {
                PlusPack.nmap.queue.head = NULL;
            }
        }

        XplSignalLocalSemaphore(PlusPack.nmap.semaphore);

        if (client->conn) {
            if (((client->vs = MDBCreateValueStruct(PlusPack.handle.directory, NULL)) != NULL) 
                    && ConnNegotiate(client->conn, PlusPack.nmap.ssl.context)) {
                ccode = ProcessConnection(client);

                if ((ccode != -1) && !(client->entry.flags & MSG_FLAG_PLUSPACK_PROCESSED)) {
                    if (PlusPack.flags & PLUSPACK_FLAG_ACL_ENABLED) {
                        ProcessACL(client);
                    } else {
                        client->flags |= (PLUSPACK_CLIENT_FLAG_FOUND | PLUSPACK_CLIENT_FLAG_ALLOWED);
                    }

                    if (PlusPack.flags & PLUSPACK_FLAG_BIGBROTHER_ENABLED) {
                        ccode = ProcessCopy(client);
                    }

                    if ((ccode != -1) && (PlusPack.flags & (PLUSPACK_FLAG_SIGNATURE_HTML | PLUSPACK_FLAG_SIGNATURE | PLUSPACK_FLAG_NO_ATTACHMENTS))) {
                        ccode = ProcessMime(client);
                    }

                    if ((ccode != -1) && (PlusPack.flags & PLUSPACK_FLAG_NO_ATTACHMENTS)) {
                        ccode = ProcessAttachments(client);
                    }

                    if ((ccode != -1) && (PlusPack.flags & PLUSPACK_FLAG_ACL_ENABLED)) {
                        ccode = ProcessAllowed(client);
                    }

                    if (ccode != -1) {
                        if (!client->bounce) {
                            if (PlusPack.flags & (PLUSPACK_FLAG_SIGNATURE_HTML | PLUSPACK_FLAG_SIGNATURE)) {
                                ccode = ProcessSignatures(client);
                            } else {
                                ccode = ProcessEnvelope(client);
                            }
                        } else {
                            ccode = NMAPSendCommandF(client->conn, "QRTS %s %s %d %d %s\r\n", client->sender.claim, client->sender.claim, DSN_HEADER | DSN_FAILURE, DELIVER_BLOCKED, client->bounce);
                        }
                    }

                    if (client->flags & PLUSPACK_CLIENT_FLAG_SPLIT) {
                        if ((ccode = NMAPSendCommand(client->conn, "QRUN\r\n", 6)) != -1) {
                            ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
                        }
                    }
                }

                if ((ccode != -1) 
                        && ((ccode = NMAPSendCommand(client->conn, "QDONE\r\n", 7)) != -1)) {
                    ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
                }
            } else {
                if (!client->vs) {
                    LoggerEvent(PlusPack.handle.directory, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, sizeof(MDBValueStruct), __LINE__, NULL, 0);
                }

                NMAPSendCommand(client->conn, "QDONE\r\n", 7);
                NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
            }
        }

        if (client->conn) {
            ConnFlush(client->conn);
        }

        FreeClientData(client);

        /* Live or die? */
        if (threadNumber == XplSafeRead(PlusPack.nmap.queue.active)) {
            if ((wokeup - sleep) > PlusPack.nmap.sleepTime) {
                break;
            }
        }

        sleep = time(NULL);

        PlusPackClientAllocCB(client, NULL);
    } while (PlusPack.state == PLUSPACK_STATE_RUNNING);

    FreeClientData(client);

    PlusPackClientFree(client);

    XplSafeDecrement(PlusPack.nmap.queue.active);

    XplExitThread(TSR_THREAD, 0);

    return;
}

static void 
PlusPackQueueServer5(void *ignored)
{
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSafeIncrement(PlusPack.server.active);

    XplRenameThread(XplGetThreadID(), "PlusPack Queue 6 Server");

    while (PlusPack.state < PLUSPACK_STATE_STOPPING) {
        if (ConnAccept(PlusPack.nmap.conn.queue6, &conn) != -1) {
            if (PlusPack.state < PLUSPACK_STATE_STOPPING) {
                conn->ssl.enable = FALSE;

                QUEUE_WORK_TO_DO(conn, id, ccode);
                if (!ccode) {
                    XplSignalLocalSemaphore(PlusPack.nmap.queue.todo);

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
                if (PlusPack.state < PLUSPACK_STATE_STOPPING) {
                    LoggerEvent(PlusPack.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }

            default: {
                if (PlusPack.state < PLUSPACK_STATE_STOPPING) {
                    XplConsolePrintf("bongopluspack: Exiting after an accept() failure; error %d\r\n", errno);

                    LoggerEvent(PlusPack.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                    PlusPack.state = PLUSPACK_STATE_STOPPING;
                }

                break;
            }
        }

        break;
    }

    /* Shutting down */
    PlusPack.state = PLUSPACK_STATE_UNLOADING;

    XplConsolePrintf("bongopluspack: Remote recipient handler shutting down.\r\n");

    XplSafeDecrement(PlusPack.server.active);
    XplSetThreadGroupID(id);

    return;
}

static void 
PlusPackQueueServer6(void *ignored)
{
    int i;
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSafeIncrement(PlusPack.server.active);

    XplRenameThread(XplGetThreadID(), "PlusPack Queue 5 Server");

    while (PlusPack.state < PLUSPACK_STATE_STOPPING) {
        if (ConnAccept(PlusPack.nmap.conn.queue5, &conn) != -1) {
            if (PlusPack.state < PLUSPACK_STATE_STOPPING) {
                conn->ssl.enable = FALSE;

                QUEUE_WORK_TO_DO(conn, id, ccode);
                if (!ccode) {
                    XplSignalLocalSemaphore(PlusPack.nmap.queue.todo);

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
                if (PlusPack.state < PLUSPACK_STATE_STOPPING) {
                    LoggerEvent(PlusPack.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }

            default: {
                if (PlusPack.state < PLUSPACK_STATE_STOPPING) {
                    XplConsolePrintf("bongopluspack: Exiting after an accept() failure; error %d\r\n", errno);

                    LoggerEvent(PlusPack.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                    PlusPack.state = PLUSPACK_STATE_STOPPING;
                }

                break;
            }
        }

        break;
    }

    /* Shutting down */
    PlusPack.state = PLUSPACK_STATE_UNLOADING;

    XplConsolePrintf("bongopluspack: Shutting down.\r\n");

    id = XplSetThreadGroupID(PlusPack.id.group);

    if (PlusPack.nmap.conn.queue5) {
        ConnClose(PlusPack.nmap.conn.queue5, 1);
        PlusPack.nmap.conn.queue5 = NULL;
    }

    if (PlusPack.nmap.conn.queue6) {
        ConnClose(PlusPack.nmap.conn.queue6, 1);
        PlusPack.nmap.conn.queue6 = NULL;
    }

    if (PlusPack.nmap.ssl.enable) {
        PlusPack.nmap.ssl.enable = FALSE;

        if (PlusPack.nmap.ssl.conn) {
            ConnClose(PlusPack.nmap.ssl.conn, 1);
            PlusPack.nmap.ssl.conn = NULL;
        }

        if (PlusPack.nmap.ssl.context) {
            ConnSSLContextFree(PlusPack.nmap.ssl.context);
            PlusPack.nmap.ssl.context = NULL;
        }
    }

    ConnCloseAll(1);

    if (ManagementState() == MANAGEMENT_RUNNING) {
        ManagementShutdown();
    }

    for (i = 0; (XplSafeRead(PlusPack.server.active) > 1) && (i < 60); i++) {
        XplDelay(1000);
    }

    for (i = 0; (ManagementState() != MANAGEMENT_STOPPED) && (i < 60); i++) {
        XplDelay(1000);
    }

    XplConsolePrintf("bongopluspack: Shutting down %d queue threads\r\n", XplSafeRead(PlusPack.nmap.queue.active));

    XplWaitOnLocalSemaphore(PlusPack.nmap.semaphore);

    ccode = XplSafeRead(PlusPack.nmap.queue.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(PlusPack.nmap.queue.todo);
    }

    XplSignalLocalSemaphore(PlusPack.nmap.semaphore);

    for (i = 0; XplSafeRead(PlusPack.nmap.queue.active) && (i < 60); i++) {
        XplDelay(1000);
    }

    if (XplSafeRead(PlusPack.server.active) > 1) {
        XplConsolePrintf("bongopluspack: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(PlusPack.server.active) - 1);
    }

    if (XplSafeRead(PlusPack.nmap.queue.active)) {
        XplConsolePrintf("bongopluspack: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(PlusPack.nmap.queue.active));
    }

    LoggerClose(PlusPack.handle.logging);
    PlusPack.handle.logging = NULL;

    XplCloseLocalSemaphore(PlusPack.nmap.queue.todo);
    XplCloseLocalSemaphore(PlusPack.nmap.semaphore);

    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    if (PlusPack.signature.plain) {
        MemFree(PlusPack.signature.plain);
    }

    if (PlusPack.signature.html) {
        MemFree(PlusPack.signature.html);
    }

    if (PlusPack.allowedGroup) {
        MemFree(PlusPack.allowedGroup);
    }

    MemPrivatePoolFree(PlusPack.nmap.pool);

    MemoryManagerClose(PLUSPACK_AGENT);

    XplConsolePrintf("bongopluspack: Shutdown complete\r\n");

    XplSignalLocalSemaphore(PlusPack.sem.main);
    XplWaitOnLocalSemaphore(PlusPack.sem.shutdown);

    XplCloseLocalSemaphore(PlusPack.sem.shutdown);
    XplCloseLocalSemaphore(PlusPack.sem.main);

    XplSetThreadGroupID(id);

    return;
}

static BOOL 
ReadConfiguration(void)
{
    unsigned char *ptr;
    MDBValueStruct *vs;

    vs = MDBCreateValueStruct(PlusPack.handle.directory, MsgGetServerDN(NULL));
    if (vs) {
        if (MDBRead(MDB_CURRENT_CONTEXT, MSGSRV_A_OFFICIAL_NAME, vs)) {
            strcpy(PlusPack.officialName, vs->Value[0]);
            MDBFreeValues(vs);
        } else {
            PlusPack.officialName[0] = '\0';
        }

        if (MDBRead(PLUSPACK_AGENT, MSGSRV_A_CONFIGURATION, vs)) {
            PlusPack.flags = atol(vs->Value[0]);
            MDBFreeValues(vs);
        }

        if (MDBRead(PLUSPACK_AGENT, MSGSRV_A_LIST_SIGNATURE, vs)) {
            if (vs->Value[0][0] 
                    && ((PlusPack.signature.plain = MemStrdup(vs->Value[0])) != NULL)) {
                PlusPack.flags |= PLUSPACK_FLAG_SIGNATURE;
            } else {
                PlusPack.flags &= ~PLUSPACK_FLAG_SIGNATURE;
            }

            MDBFreeValues(vs);
        }

        if (MDBRead(PLUSPACK_AGENT, MSGSRV_A_LIST_SIGNATURE_HTML, vs)) {
            if (vs->Value[0][0] 
                    && ((PlusPack.signature.html = MemStrdup(vs->Value[0])) != NULL)) {
                PlusPack.flags |= PLUSPACK_FLAG_SIGNATURE_HTML;
            } else {
                PlusPack.flags &= ~PLUSPACK_FLAG_SIGNATURE_HTML;
            }

            MDBFreeValues(vs);
        }

        if (MDBReadDN(PLUSPACK_AGENT, PLUSPACK_A_COPY_USER, vs)) {
            if (vs->Value[0][0] 
                    && (strlen(vs->Value[0]) < sizeof(PlusPack.copy.user))) {
                if (!(ptr = strrchr(vs->Value[0], '\\'))) {
                    strcpy(PlusPack.copy.user, vs->Value[0]);
                } else {
                    strcpy(PlusPack.copy.user, ptr + 1);
                }

                PlusPack.flags |= PLUSPACK_FLAG_BIGBROTHER_ENABLED;
            } else {
                PlusPack.flags &= ~PLUSPACK_FLAG_BIGBROTHER_ENABLED;
            }

            MDBFreeValues(vs);
        }

        PlusPack.copy.inbox[0] = '\0';
        PlusPack.copy.outbox[0] = '\0';
        if (MDBRead(PLUSPACK_AGENT, PLUSPACK_A_COPY_MBOX, vs)) {
            if (vs->Value[0][0] 
                    && (strlen(vs->Value[0]) < sizeof(PlusPack.copy.inbox))) {
                strcpy(PlusPack.copy.inbox, vs->Value[0]);
            }

            if ((vs->Used == 2) 
                    && vs->Value[1][0] 
                    && (strlen(vs->Value[1]) < sizeof(PlusPack.copy.outbox))) {
                strcpy(PlusPack.copy.outbox, vs->Value[1]);
            } else {
                strcpy(PlusPack.copy.outbox, PlusPack.copy.inbox);
            }

            MDBFreeValues(vs);
        }

        if (MDBReadDN(PLUSPACK_AGENT, PLUSPACK_A_ACL_GROUP, vs)) {
            if (vs->Value[0][0]) {
                PlusPack.allowedGroup = vs->Value[0];
                vs->Used = 0;

                PlusPack.flags |= PLUSPACK_FLAG_ACL_ENABLED;
            } else {
                PlusPack.flags &= ~PLUSPACK_FLAG_ACL_ENABLED;
            }

            MDBFreeValues(vs);
        }

        MDBDestroyValueStruct(vs);

        return(TRUE);
    }

    return(FALSE);
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = TRUE;
        PlusPack.state = PLUSPACK_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(PlusPack.sem.shutdown);

        id = XplSetThreadGroupID(PlusPack.id.group);
        ConnClose(PlusPack.nmap.conn.queue5, 1);
        XplSetThreadGroupID(id);

        id = XplSetThreadGroupID(PlusPack.id.group);
        ConnClose(PlusPack.nmap.conn.queue6, 1);
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(PlusPack.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (PlusPack.state < PLUSPACK_STATE_UNLOADING) {
                PlusPack.state = PLUSPACK_STATE_UNLOADING;
            }

            break;
        }

        case SIGINT:
        case SIGTERM: {
            if (PlusPack.state == PLUSPACK_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (PlusPack.state < PLUSPACK_STATE_STOPPING) {
                PlusPack.state = PLUSPACK_STATE_STOPPING;
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
    if (PlusPack.flags & PLUSPACK_FLAG_BIGBROTHER_ENABLED) {
        PlusPack.nmap.conn.queue5 = ConnAlloc(FALSE);
    }

    PlusPack.nmap.conn.queue6 = ConnAlloc(FALSE);
    if (PlusPack.nmap.conn.queue6 && (!(PlusPack.flags & PLUSPACK_FLAG_BIGBROTHER_ENABLED) || PlusPack.nmap.conn.queue5)) {
        memset(&(PlusPack.nmap.conn.queue6->socketAddress), 0, sizeof(PlusPack.nmap.conn.queue6->socketAddress));
        PlusPack.nmap.conn.queue6->socketAddress.sin_family = AF_INET;
        PlusPack.nmap.conn.queue6->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

        if (PlusPack.flags & PLUSPACK_FLAG_BIGBROTHER_ENABLED) {
            memset(&(PlusPack.nmap.conn.queue5->socketAddress), 0, sizeof(PlusPack.nmap.conn.queue5->socketAddress));
            PlusPack.nmap.conn.queue5->socketAddress.sin_family = AF_INET;
            PlusPack.nmap.conn.queue5->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
        }

        /* Get root privs back for the bind.  It's ok if this fails -
           the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);

        if (PlusPack.flags & PLUSPACK_FLAG_BIGBROTHER_ENABLED) {
            PlusPack.nmap.conn.queue5->socket = ConnServerSocket(PlusPack.nmap.conn.queue5, 2048);
        }

        PlusPack.nmap.conn.queue6->socket = ConnServerSocket(PlusPack.nmap.conn.queue6, 2048);

        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongopluspack: Could not drop to unprivileged user '%s'\r\n", MsgGetUnprivilegedUser());
            if (PlusPack.nmap.conn.queue5) {
                ConnFree(PlusPack.nmap.conn.queue5);
                PlusPack.nmap.conn.queue5 = NULL;
            }

            ConnFree(PlusPack.nmap.conn.queue6);
            PlusPack.nmap.conn.queue6 = NULL;
            return(-1);
        }

        if ((PlusPack.nmap.conn.queue5->socket == -1) || (PlusPack.nmap.conn.queue6->socket == -1)) {
            XplConsolePrintf("bongopluspack: Could not bind to dynamic port\r\n");
            if (PlusPack.nmap.conn.queue5) {
                ConnFree(PlusPack.nmap.conn.queue5);
                PlusPack.nmap.conn.queue5 = NULL;
            }

            ConnFree(PlusPack.nmap.conn.queue6);
            PlusPack.nmap.conn.queue6 = NULL;
            return(-1);
        }

        if ((NMAPRegister(PLUSPACK_AGENT, Q_FIVE, PlusPack.nmap.conn.queue5->socketAddress.sin_port) != REGISTRATION_COMPLETED) 
                || (NMAPRegister(PLUSPACK_AGENT, Q_DELIVER, PlusPack.nmap.conn.queue6->socketAddress.sin_port) != REGISTRATION_COMPLETED)) {
            XplConsolePrintf("bongopluspack: Could not register with bongonmap\r\n");
            if (PlusPack.nmap.conn.queue5) {
                ConnFree(PlusPack.nmap.conn.queue5);
                PlusPack.nmap.conn.queue5 = NULL;
            }

            ConnFree(PlusPack.nmap.conn.queue6);
            PlusPack.nmap.conn.queue6 = NULL;
            return(-1);
        }
    } else {
        if (PlusPack.nmap.conn.queue5) {
            ConnFree(PlusPack.nmap.conn.queue5);
            PlusPack.nmap.conn.queue5 = NULL;
        }

        XplConsolePrintf("bongopluspack: Could not allocate connection.\r\n");
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
        XplConsolePrintf("bongopluspack: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
        return(1);
    }
    XplInit();

    XplSignalHandler(SignalHandler);

    PlusPack.id.main = XplGetThreadID();
    PlusPack.id.group = XplGetThreadGroupID();

    PlusPack.state = PLUSPACK_STATE_INITIALIZING;

    PlusPack.nmap.conn.queue5 = NULL;
    PlusPack.nmap.conn.queue6 = NULL;

    PlusPack.nmap.pool = NULL;
    PlusPack.nmap.sleepTime = (5 * 60);
    PlusPack.nmap.ssl.conn = NULL;
    PlusPack.nmap.ssl.enable = FALSE;
    PlusPack.nmap.ssl.context = NULL;
    PlusPack.nmap.ssl.config.options = 0;

    PlusPack.handle.directory = NULL;
    PlusPack.handle.logging = NULL;

    strcpy(PlusPack.nmap.address, "127.0.0.1");

    XplSafeWrite(PlusPack.server.active, 0);

    XplSafeWrite(PlusPack.nmap.queue.idle, 0);
    XplSafeWrite(PlusPack.nmap.queue.active, 0);
    XplSafeWrite(PlusPack.nmap.queue.maximum, 100000);

    if (MemoryManagerOpen(PLUSPACK_AGENT) == TRUE) {
        PlusPack.nmap.pool = MemPrivatePoolAlloc("PlusPack Connections", sizeof(PlusPackClient), 0, 3072, TRUE, FALSE, PlusPackClientAllocCB, NULL, NULL);
        if (PlusPack.nmap.pool != NULL) {
            XplOpenLocalSemaphore(PlusPack.sem.main, 0);
            XplOpenLocalSemaphore(PlusPack.sem.shutdown, 1);
            XplOpenLocalSemaphore(PlusPack.nmap.semaphore, 1);
            XplOpenLocalSemaphore(PlusPack.nmap.queue.todo, 1);
        } else {
            MemoryManagerClose(PLUSPACK_AGENT);

            XplConsolePrintf("bongopluspack: Unable to create connection pool; shutting down.\r\n");
            return(-1);
        }
    } else {
        XplConsolePrintf("bongopluspack: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    ConnStartup(CONNECTION_TIMEOUT, TRUE);

    MDBInit();
    PlusPack.handle.directory = (MDBHandle)MsgInit();
    if (PlusPack.handle.directory == NULL) {
        XplBell();
        XplConsolePrintf("bongopluspack: Invalid directory credentials; exiting!\r\n");
        XplBell();

        MemoryManagerClose(PLUSPACK_AGENT);

        return(-1);
    }

    /*
        We need to delay the startup of the plus-pack agent to allow SMTP to register with NMAP first.

        fixme - when enhanced nmap registration is available; remove this delay!
    */
#if 0
    XplDelay(20 * 1000);
#endif

    NMAPInitialize(PlusPack.handle.directory);

    SetCurrentNameSpace(NWOS2_NAME_SPACE);
    SetTargetNameSpace(NWOS2_NAME_SPACE);

    PlusPack.handle.logging = LoggerOpen("bongopluspack");
    if (!PlusPack.handle.logging) {
        XplConsolePrintf("bongopluspack: Unable to initialize logging; disabled.\r\n");
    }

    ReadConfiguration();

    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "pluspack");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    if (PlusPack.flags) {
        if (QueueSocketInit() < 0) {
            XplConsolePrintf("bongopluspack: Exiting.\r\n");

            MemoryManagerClose(PLUSPACK_AGENT);

            return -1;
        }

        if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongopluspack: Could not drop to unprivileged user '%s', exiting.\r\n", MsgGetUnprivilegedUser());

            MemoryManagerClose(PLUSPACK_AGENT);

            return 1;
        }

        PlusPack.nmap.ssl.enable = FALSE;
        PlusPack.nmap.ssl.config.method = SSLv23_client_method;
        PlusPack.nmap.ssl.config.options = SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS;
        PlusPack.nmap.ssl.config.mode = SSL_MODE_AUTO_RETRY;
        PlusPack.nmap.ssl.config.cipherList = NULL;
        PlusPack.nmap.ssl.config.certificate.type = SSL_FILETYPE_PEM;
        PlusPack.nmap.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
        PlusPack.nmap.ssl.config.key.type = SSL_FILETYPE_PEM;
        PlusPack.nmap.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

        PlusPack.nmap.ssl.context = ConnSSLContextAlloc(&(PlusPack.nmap.ssl.config));
        if (PlusPack.nmap.ssl.context) {
            PlusPack.nmap.ssl.enable = TRUE;
        }

        NMAPSetEncryption(PlusPack.nmap.ssl.context);

        if ((ManagementInit(PLUSPACK_AGENT, PlusPack.handle.directory)) 
                && (ManagementSetVariables(GetPlusPackManagementVariables(), GetPlusPackManagementVariablesCount())) 
                && (ManagementSetCommands(GetPlusPackManagementCommands(), GetPlusPackManagementCommandsCount()))) {
            XplBeginThread(&id, ManagementServer, DMC_MANAGEMENT_STACKSIZE, NULL, ccode);
        } else {
            ccode = -1;
        }

        if (ccode) {
            XplConsolePrintf("bongopluspack: Unable to startup the management interface.\r\n");
        }

        PlusPack.state = PLUSPACK_STATE_RUNNING;

        if (PlusPack.flags & PLUSPACK_FLAG_BIGBROTHER_ENABLED) {
            XplBeginThread(&id, PlusPackQueueServer5, 8192, NULL, ccode);
        } else {
            ccode = 0;
        }

        if (!ccode) {
            XplStartMainThread(PRODUCT_SHORT_NAME, &id, PlusPackQueueServer6, 8192, NULL, ccode);
        } else {
            XplConsolePrintf("bongopluspack: Unable to startup an NMAP queue server thread.\r\n");
        }
    } else {
        XplConsolePrintf("bongopluspack: Not configured; unloading.\r\n");
    }
        
    XplUnloadApp(XplGetThreadID());
    return(0);
}
