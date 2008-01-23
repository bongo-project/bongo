/***************************************************************************
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

#include <config.h>

/* Compile-time options */
#define	NETDB_USE_INTERNET	1
#undef	USE_HOPCOUNT_DETECTION

#include <xpl.h>
#include <xpldns.h>
#include <connio.h>
#include <nmlib.h>

#include <msgapi.h>

#define LOGGERNAME "smtpd"
#define PRODUCT_NAME            "Bongo SMTP Agent"
#define PRODUCT_VERSION         "$Revision: 1.7 $"

#include <logger.h>

#include <nmap.h>

#include <bongoagent.h>
#include <bongoutil.h>
#include "smtpd.h"

struct {
	int port;
	int port_ssl;
	BOOL allow_client_ssl;
	BOOL allow_expn;
	BOOL allow_auth;
    BOOL require_auth;
	BOOL allow_vrfy;
	BOOL verify_address;
	BOOL accept_etrn;
	BOOL send_etrn;
	int max_recips;
	int message_limit;
	BOOL use_relay;
	BOOL relay_local;
	char *relay_host;
	BOOL block_rts;
	int max_thread_load;
	int max_flood_count;
	int max_null_sender;
	int max_mx_servers;
	int socket_timeout;
} SMTP;

static BongoConfigItem SMTPConfig[] = {
	{ BONGO_JSON_INT, "o:port/i", &SMTP.port },
	{ BONGO_JSON_INT, "o:port_ssl/i", &SMTP.port_ssl },
	{ BONGO_JSON_BOOL, "o:allow_client_ssl/b", &SMTP.allow_client_ssl },
	{ BONGO_JSON_BOOL, "o:allow_expn/b", &SMTP.allow_expn },
	{ BONGO_JSON_BOOL, "o:allow_vrfy/b", &SMTP.allow_vrfy },
	{ BONGO_JSON_BOOL, "o:allow_auth/b", &SMTP.allow_auth },
	{ BONGO_JSON_BOOL, "o:verify_address/b", &SMTP.verify_address },
	{ BONGO_JSON_BOOL, "o:accept_etrn/b", &SMTP.accept_etrn },
	{ BONGO_JSON_BOOL, "o:send_etrn/b", &SMTP.send_etrn },
	{ BONGO_JSON_INT, "o:maximum_recipients/i", &SMTP.max_recips },
	{ BONGO_JSON_INT, "o:message_size_limit/i", &SMTP.message_limit },
	{ BONGO_JSON_BOOL, "o:use_relay_host/b", &SMTP.use_relay },
	{ BONGO_JSON_STRING, "o:relay_host/s", &SMTP.relay_host },
	{ BONGO_JSON_BOOL, "o:block_rts_spam/b", &SMTP.block_rts },
	{ BONGO_JSON_INT, "o:max_thread_load/i", &SMTP.max_thread_load },
	{ BONGO_JSON_INT, "o:max_flood_count/i", &SMTP.max_flood_count }, // UNUSED ?
	{ BONGO_JSON_INT, "o:max_null_sender/i", &SMTP.max_null_sender },
	{ BONGO_JSON_INT, "o:socket_timeout/i", &SMTP.socket_timeout },
	{ BONGO_JSON_INT, "o:max_mx_servers/i", &SMTP.max_mx_servers },
	{ BONGO_JSON_BOOL, "o:relay_local_mail/b", &SMTP.relay_local },
    { BONGO_JSON_BOOL, "o:require_auth/b", &SMTP.require_auth },
	{ BONGO_JSON_NULL, NULL, NULL }
};

/* Globals */
BOOL Exiting = FALSE;
XplSemaphore SMTPShutdownSemaphore;
XplSemaphore SMTPServerSemaphore;
XplAtomic SMTPServerThreads;
XplAtomic SMTPConnThreads;
XplAtomic SMTPIdleConnThreads;
XplAtomic SMTPQueueThreads;
Connection *SMTPServerConnection;
Connection *SMTPServerConnectionSSL;
Connection *SMTPQServerConnection;
Connection *SMTPQServerConnectionSSL;
int TGid;

/* UBE measures */
XplRWLock ConfigLock;
BOOL SMTPReceiverStopped = FALSE;

BOOL Notify = FALSE;
BOOL AllowClientSSL = FALSE;
BOOL DomainUsernames = TRUE;
time_t LastBounce;
time_t BounceInterval;
unsigned long MaxBounceCount;
unsigned long BounceCount;
unsigned char NMAPServer[20] = "127.0.0.1";
unsigned long LocalAddress = 0;
bongo_ssl_context *SSLContext = NULL;
static unsigned char NMAPHash[NMAP_HASH_SIZE];

#ifdef USE_HOPCOUNT_DETECTION
int MAX_HOPS = 16;
#endif

#define	BUFSIZE					1023
#define	LINESIZE					BUFSIZE-10
#define	MTU						1536*3

#define	MAX_USERPART_LEN		127

#define	STACKSIZE_Q				128000  /* 32767 */
#define	STACKSIZE_S 128000  /* 32767 */

/* Values other than these are no longer supported */
#define	UBE_REMOTE_AUTH_ONLY    (1<<3)
#define	UBE_SMTP_AFTER_POP      (1<<5)
#define	UBE_DISABLE_AUTH        (1<<8)
#define	UBE_DEFAULT_NOT_TRUSTED (UBE_REMOTE_AUTH_ONLY | UBE_SMTP_AFTER_POP)

/* Minutes * 60 (1 second granularity) */
/*#define       CONNECTION_TIMEOUT      (10*60)*/
/* Seconds */
#define	MAILBOX_TIMEOUT 15

#define ChopNL(String) { unsigned char *pTr; pTr=strchr((String), 0x0a); if (pTr) *pTr='\0'; pTr=strrchr((String), 0x0d); if (pTr) *pTr='\0'; }

struct __InternalConnectionStruct {
    Connection      *conn;
    char            *buffer;
    unsigned long   buflen;
    BOOL            ssl;
};

typedef struct
{
    struct __InternalConnectionStruct client;
    struct __InternalConnectionStruct nmap;
    struct __InternalConnectionStruct remotesmtp;

    unsigned long State;        /* Connection state     */
    unsigned long Flags;        /* Status flags         */
    unsigned long RecipCount;   /* Number of recipients */
    NMAPMessageFlags MsgFlags;  /* current msg flags    */

    unsigned char RemoteHost[MAXEMAILNAMESIZE + 1]; /* Name of remote host   */
    unsigned char *Command;             /* Current command       */
    unsigned char *From;                            /* For Routing Disabled  */
    unsigned char *AuthFrom;                     /* Sender Authenticated As  */
    unsigned char User[64];                      /* Fixme - Make this bigger */
    BOOL IsEHLO;                                /* used for RFC 3848 */
} ConnectionStruct;

typedef struct
{
    unsigned char To[MAXEMAILNAMESIZE+1];
    unsigned char ORecip[MAXEMAILNAMESIZE+1];
    unsigned long Flags;
    int Result;
} RecipStruct;

/* Encoding constants: none (unknown), 7bit, 8bitmime, binarymime, binary */
#define	CODE_NONE  0
#define	CODE_7BIT  1
#define	CODE_8BITM 2
#define	CODE_BINM  3
#define	CODE_BIN   4

#define	SENDER_LOCAL (1<<10)

#if !defined(DEBUG)
#define	GetSMTPConnection() MemPrivatePoolGetEntry(SMTPConnectionPool)
#else
#define	GetSMTPConnection() MemPrivatePoolGetEntryDebug(SMTPConnectionPool, __FILE__, __LINE__)
#endif

void *SMTPConnectionPool = NULL;

/* Prototypes */
long ReadConnection(Connection *conn, char **buffer, unsigned long *buflen);
void ProcessRemoteEntry (ConnectionStruct * Client, unsigned long Size, int Lines);
int RewriteAddress (Connection * conn, unsigned char *Source, unsigned char *Target, unsigned int TargetSize);
BOOL FlushClient (ConnectionStruct * Client);
BOOL EndClientConnection (ConnectionStruct * Client);
static int PullLine (unsigned char *Line, unsigned long LineSize, unsigned char **NextLine);
/* this PullLine will strip off the crlf where the other one only seems to strip off the lf
 * eventually i'd like to remove the first and convert everything to using the straight connio
 * stuff for speed and security */
static int PullLine2 (unsigned char *Line, unsigned long LineSize, unsigned char **NextLine);

static BOOL
SMTPConnectionAllocCB (void *Buffer, void *ClientData)
{
    ConnectionStruct *c = (ConnectionStruct *) Buffer;

    memset (c, 0, sizeof (ConnectionStruct));
    c->State = STATE_FRESH;
    c->MsgFlags = MSG_FLAG_ENCODING_NONE;

    return (TRUE);
}

static void
ReturnSMTPConnection (ConnectionStruct * Client)
{
    ConnectionStruct *c = Client;

    memset (c, 0, sizeof (ConnectionStruct));
    c->State = STATE_FRESH;
    c->MsgFlags = MSG_FLAG_ENCODING_NONE;


    MemPrivatePoolReturnEntry (c);

    return;
}

__inline static unsigned char *
strchrRN (unsigned char *Buffer, unsigned char SrchChar,
          unsigned char *EndPtr)
{
    unsigned char *ptr = Buffer;
    unsigned char srchChar = SrchChar;

    do {
        while (*ptr != '\0') {
            if (*ptr != srchChar) {
                ptr++;
                continue;
            }
            else {
                return (ptr);
            }
        }

        if (ptr == EndPtr) {
            return (NULL);
        }

        *ptr = ' ';
        ptr++;
    } while (ptr < EndPtr);

    return (NULL);
}

#define FreeInternalConnection(c)           \
    if (c.conn) {                           \
        ConnClose(c.conn, 1);               \
        ConnFree(c.conn);                   \
        c.conn = NULL;                      \
                                            \
        if (c.buffer) {                     \
            MemFree(c.buffer);              \
            c.buffer = NULL;                \
            c.buflen = 0;                   \
        }                                   \
    }                                       \

BOOL
EndClientConnection (ConnectionStruct * Client)
{
    unsigned char Reply[BUFSIZE + 1];

    if (Client) {
        if (Client->State == STATE_ENDING) {
            return (TRUE);
        }

        Client->Flags = Client->State;  /* abusing the field - saves stack */
        Client->State = STATE_ENDING;

        if (Client->From) {
            MemFree (Client->From);
            Client->From = NULL;
        }

        if (Client->nmap.conn) {
            if (Client->Flags != STATE_HELO) {
                /* if smtp is in the middle of receiving a message,   */
                /* we need to send a dot on a line by itself          */
                /* to get NMAP out of the receiving state and into    */
                /* the command state so we can issue the QABRT and    */
                /* QUIT commands.  If NMAP is already in the command  */
                /* state, the dot on a line by itself will not hurt   */
                /* anything.  NMAP will just send 'unknown command'   */
                NMAPSendCommand (Client->nmap.conn, "\r\n.\r\n", 5);
                NMAPReadResponse(Client->nmap.conn, Reply, sizeof(Reply), TRUE); //empty line
                NMAPReadResponse(Client->nmap.conn, Reply, sizeof(Reply), TRUE); //.
                NMAPSendCommand (Client->nmap.conn, "QABRT\r\nQUIT\r\n", 13);
                NMAPReadResponse(Client->nmap.conn, Reply, sizeof(Reply), TRUE); //qabrt which we've already done once....
                NMAPReadResponse(Client->nmap.conn, Reply, sizeof(Reply), TRUE); //quit
            } else {
                NMAPSendCommand (Client->nmap.conn, "QUIT\r\n", 6);
                NMAPReadResponse(Client->nmap.conn, Reply, sizeof(Reply), TRUE); //quit
            }
            FreeInternalConnection(Client->nmap);
        }

        if (Client->client.conn) {
            FreeInternalConnection(Client->client);
        }

        if (Client->remotesmtp.conn) {
            ConnWrite(Client->remotesmtp.conn, "RSET\r\nQUIT\r\n", 12);
            FreeInternalConnection(Client->remotesmtp);
        }

        if (Client->AuthFrom != NULL) {
            MemFree (Client->AuthFrom);
            Client->AuthFrom = NULL;
        }

        /* Bump our thread count */
        if (Client->Flags < STATE_WAITING) {
            XplSafeDecrement (SMTPConnThreads);
        } else {
            XplSafeDecrement (SMTPQueueThreads);
        }

        ReturnSMTPConnection (Client);
    }
    XplExitThread (TSR_THREAD, 0);
    return (FALSE);
}

static BOOL
HandleConnection (void *param)
{
    ConnectionStruct *Client = (ConnectionStruct *) param;
    int count;
    int ReplyInt;
    BOOL Ready;
    BOOL Working = TRUE;
    BOOL IsTrusted = TRUE;
    BOOL IsAuthed = FALSE;
    BOOL AllowAuth = TRUE;
    BOOL NullSender = FALSE;
    BOOL TooManyNullSenderRecips = FALSE;
    unsigned char *ptr, *ptr2;
    unsigned char Answer[BUFSIZE + 1];
    unsigned char Reply[BUFSIZE + 1];
    struct sockaddr_in soc_address;
    struct sockaddr_in *sin = &soc_address;
    time_t connectionTime;

    time (&connectionTime);

    if (Client->client.conn->ssl.enable) {
        Log(LOG_INFO, "SSL connection from %s", LOGIP(Client->client.conn->socketAddress));
        if (!ConnNegotiate(Client->client.conn, SSLContext)) {
            return (EndClientConnection (Client));
        }
    } else {
        Log(LOG_INFO, "Connection from %s", LOGIP(Client->client.conn->socketAddress));
    }

    XplRWReadLockAcquire (&ConfigLock);
    if (! SMTP.allow_auth) {
        AllowAuth = FALSE;
    }

    XplRWReadLockRelease (&ConfigLock);

    /* Connect to Queue agent */
    sin->sin_addr.s_addr=inet_addr(NMAPServer);
    sin->sin_family=AF_INET;
    sin->sin_port=htons(BONGO_QUEUE_PORT);
    if ((Client->nmap.conn = NMAPConnectEx(NULL, sin, Client->client.conn->trace.destination)) == NULL ||
        !NMAPAuthenticateToStore(Client->nmap.conn, Answer, sizeof(Answer))) {
        if (Client->nmap.conn) {
            NMAPQuit(Client->nmap.conn);
            Client->nmap.conn = NULL;
            Log(LOG_ERROR, "Unable to authenticate to Queue agent at %s",
                LOGIP(Client->client.conn->socketAddress));
        } else {
            Log(LOG_ERROR, "Unable to connect to Queue agent at %s (reply was %d)",
                LOGIP(Client->client.conn->socketAddress), ReplyInt);
        }
        
        count = snprintf(Reply, sizeof(Reply), "421 %s %s\r\n", BongoGlobals.hostname, MSG421SHUTDOWN);
        
        ConnWrite (Client->client.conn, Reply, count);
        ConnFlush (Client->client.conn);
        return (EndClientConnection (Client));
    }

    snprintf (Answer, sizeof (Answer), "220 %s %s %s\r\n", BongoGlobals.hostname, MSG220READY, PRODUCT_VERSION);
    ConnWrite (Client->client.conn, Answer, strlen (Answer));
    ConnFlush (Client->client.conn);

    while (Working) {
        Ready = FALSE;
        while (!Ready) {
            if (Exiting == FALSE) {
                count = ConnReadToAllocatedBuffer(Client->client.conn, &(Client->client.buffer), &(Client->client.buflen));
            } else {
                snprintf (Answer, sizeof (Answer), "421 %s %s\r\n", BongoGlobals.hostname, MSG421SHUTDOWN);
                ConnWrite (Client->client.conn, Answer, strlen (Answer));
                ConnFlush(Client->client.conn);
                return (EndClientConnection (Client));
            }

            if (count > 0) {
            /* this simplifies the rest of the code */
                Client->Command = Client->client.buffer;
                Ready = TRUE;
            } else if (Exiting == FALSE) {
                Log(LOG_ERROR, "Connection to %s timed out (time: %d)",
                    LOGIP(soc_address), time(NULL) - connectionTime);
                return (EndClientConnection (Client));
            } else if (count < 1) {
                snprintf (Answer, sizeof (Answer), "421 %s %s\r\n", BongoGlobals.hostname, MSG421SHUTDOWN);
                ConnWrite (Client->client.conn, Answer, strlen (Answer));
                ConnFlush(Client->client.conn);
                return (EndClientConnection (Client));
            }
        }

        switch (toupper (Client->Command[0])) {
        case 'H':
            switch (toupper (Client->Command[3])) {
            case 'O':          /* HELO */
                if (Client->State == STATE_FRESH) {
                    snprintf (Answer, sizeof (Answer), "250 %s %s\r\n", BongoGlobals.hostname, MSG250HELLO);
                    ConnWrite (Client->client.conn, Answer, strlen (Answer));
                    strncpy (Client->RemoteHost, Client->Command + 5,
                             sizeof (Client->RemoteHost));
                    Client->State = STATE_HELO;

                    NullSender = FALSE;
                    TooManyNullSenderRecips = FALSE;
                }
                else {
                    ConnWrite (Client->client.conn, MSG503BADORDER, MSG503BADORDER_LEN);
                }
                break;

            case 'P':          /* HELP */
                ConnWrite (Client->client.conn, MSG211HELP, MSG211HELP_LEN);
                break;

            default:
                ConnWrite (Client->client.conn, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                break;
            }
            break;

        case 'S':{             /* SAML, SOML, SEND */
                switch (toupper (Client->Command[1])) {
                case 'T':{     /* STARTTLS */
                        if (!SMTP.allow_client_ssl) {
                            ConnWrite (Client->client.conn, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                            break;
                        }

                        ConnWrite (Client->client.conn, MSG220TLSREADY, MSG220TLSREADY_LEN);
                        ConnFlush (Client->client.conn);

                        /* try to negotiate the connection */
                        Client->client.conn->ssl.enable = TRUE;

                        if (ConnNegotiate(Client->client.conn, SSLContext)) {
                            /* negotiation worked */
                            Client->State = STATE_FRESH;
                        }
                        break;
                    }

                case 'A':      /* SAML */
                case 'O':      /* SOML */
                case 'E':{     /* SEND */
                        ConnWrite (Client->client.conn, MSG502NOTIMPLEMENTED,
                                    MSG502NOTIMPLEMENTED_LEN);
                        break;
                    }

                default:{
                        ConnWrite (Client->client.conn, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                        break;
                    }
                }
                break;
            }

        case 'A':{             /* AUTH */
                unsigned char *PW;

                if (AllowAuth == FALSE) {
                    ConnWrite (Client->client.conn, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                    break;
                }

                /* rfc 2554 */
                if (IsAuthed) {
                    ConnWrite (Client->client.conn, MSG503BADORDER, MSG503BADORDER_LEN);
                    break;
                } 

                if (XplStrNCaseCmp (Client->Command + 5, "LOGIN", 5) != 0) {
                    ConnWrite (Client->client.conn, MSG504BADAUTH, MSG504BADAUTH_LEN);
                    break;
                }
                if (!isspace (Client->Command[10])) {
                    ConnWrite (Client->client.conn, "334 VXNlcm5hbWU6\r\n", 18);
                    ConnFlush (Client->client.conn);
                    ConnReadToAllocatedBuffer(Client->client.conn, &(Client->client.buffer), &(Client->client.buflen));
                    strcpy (Reply, Client->client.buffer);
                }
                else {
                    strcpy (Reply, Client->client.buffer + 11);
                }

                /* it might be possible to abort the auth here (rfc 2554). 
                 * this is a code duplication from below */
                if (Client->client.buffer[0] == '*' && Client->client.buffer[1] == '\0') {
                    ConnWrite (Client->client.conn, "501 Authentication aborted by client\r\n", 38);
                    break;
                }

                ConnWrite (Client->client.conn, "334 UGFzc3dvcmQ6\r\n", 18);
                ConnFlush (Client->client.conn);

                ConnReadToAllocatedBuffer(Client->client.conn, &(Client->client.buffer), &(Client->client.buflen));

                if (Client->client.buffer[0] == '*' && Client->client.buffer[1] == '\0') {
                    ConnWrite (Client->client.conn, "501 Authentication aborted by client\r\n", 38);
                    break;
                }

                PW = DecodeBase64 (Client->client.buffer);
                DecodeBase64 (Reply);

                if (MsgAuthFindUser(Reply) == 0) {
                    if (MsgAuthVerifyPassword(Reply, PW) != 0) {
                        ConnWrite (Client->client.conn, "501 Authentication failed!\r\n", 28);
                        Log(LOG_NOTICE, "Wrong password from user %s at host %s",
                            Reply,
                            LOGIP(Client->client.conn->socketAddress));
                    } else {
                        Log(LOG_NOTICE, "Successful login for user %s at host %s",
                            Reply,
                            LOGIP(Client->client.conn->socketAddress));
                        if (Client->AuthFrom != NULL) MemFree (Client->AuthFrom);
                        Client->AuthFrom = MemStrdup (Reply);
                        Client->State = STATE_AUTH;
                        ConnWrite (Client->client.conn, "235 Authentication successful!\r\n", 32);
                        IsAuthed = TRUE;
                        IsTrusted = TRUE;
                    }
                } else {
                    Log(LOG_NOTICE, "Unknown user %s at host %s", Reply,
                        LOGIP(Client->client.conn->socketAddress));
                    ConnWrite (Client->client.conn, "501 Authentication failed!\r\n", 28);
                }
                break;
            }

        case 'R':
            switch (toupper (Client->Command[1])) {
            case 'S':          /* RSET */
                if (Client->State != STATE_FRESH) {
                    NMAPSendCommand (Client->nmap.conn, "QABRT\r\n", 7);
                    NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE);
                }
                Client->State = (Client->State) ? STATE_HELO : STATE_FRESH;
                Client->Flags = 0;
                Client->RecipCount = 0;
                Client->MsgFlags = MSG_FLAG_ENCODING_NONE;
                if (Client->From) {
                    MemFree (Client->From);
                    Client->From = NULL;
                }

                ConnWrite (Client->client.conn, MSG250OK, MSG250OK_LEN);
                break;


            case 'C':{         /* RCPT TO */
                    unsigned char temp, *name;
                    BOOL GotFlags = FALSE;
                    unsigned char To[MAXEMAILNAMESIZE + 1] = "";
                    unsigned char *Orcpt = NULL;

                    if (Client->State < STATE_FROM) {
                        ConnWrite (Client->client.conn, MSG501NOSENDER,
                                    MSG501NOSENDER_LEN);
                        break;
                    }

                    if ((ptr = strchr (Client->Command, ':')) == NULL) {
                        ConnWrite (Client->client.conn, MSG501SYNTAX, MSG501SYNTAX_LEN);
                        break;
                    }

                    if (TooManyNullSenderRecips == TRUE) {
                        XplDelay (250);

                        Client->RecipCount++;

                        ConnWrite (Client->client.conn, MSG550TOOMANY, MSG550TOOMANY_LEN);
                        break;
                    }

                    if ((ptr2 = strchr (ptr + 1, '<')) != NULL) {
                        ptr = ptr2 + 1;
                        if (*ptr == '\0') {
                            ConnWrite (Client->client.conn, MSG501SYNTAX,
                                        MSG501SYNTAX_LEN);
                            break;
                        }
                        ptr2 = strchr (ptr, '>');
                        if (ptr2) {
                            temp = '>';
                            *ptr2 = '\0';
                        }
                        else
                            temp = '\0';
                    }
                    else {
                        ptr++;
                        while (isspace (*ptr))
                            ptr++;
                        if (*ptr == '\0') {
                            ConnWrite (Client->client.conn, MSG501SYNTAX,
                                        MSG501SYNTAX_LEN);
                            break;
                        }
                        ptr2 = ptr;
                        while (*ptr2 && *ptr2 != ' ')
                            ptr2++;
                        temp = *ptr2;
                        *ptr2 = '\0';
                    }

                    name = ptr;

                    if (temp != '\0') {
                        do {
                            ptr2++;
                        } while (isspace (*ptr2));
                    }

                    while (ptr2 && *ptr2 != '\0') {
                        switch (toupper (*ptr2)) {
                        case 'N':      /* NOTIFY */
                            GotFlags = TRUE;
                            ptr2 += 6;
                            while (*ptr2 == '=' || *ptr2 == ',') {
                                ptr2++;
                                switch (toupper (*ptr2)) {
                                case 'N':
                                    ptr2 += 5;
                                    Client->Flags &=
                                        ~(DSN_SUCCESS | DSN_TIMEOUT |
                                          DSN_FAILURE);
                                    break;
                                case 'S':
                                    ptr2 += 7;
                                    Client->Flags |= DSN_SUCCESS;
                                    break;
                                case 'F':
                                    ptr2 += 7;
                                    Client->Flags |= DSN_FAILURE;
                                    break;
                                case 'D':
                                    ptr2 += 5;
                                    Client->Flags |= DSN_TIMEOUT;
                                    break;
                                default:
                                    ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                                MSG501PARAMERROR_LEN);
                                    goto QuitRcpt;
                                    break;
                                }
                            }
                            break;

                        case 'O':      /* ORCPT */
                            ptr = ptr2 + 6;
                            ptr2 = ptr;
                            while (!isspace (*ptr2) && *ptr2 != '\0')
                                ptr2++;
                            temp = *ptr2;
                            *ptr2 = '\0';

                            if (*ptr) {
                                Orcpt = MemStrdup (ptr);
                            }
                            else {
                                ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                            MSG501PARAMERROR_LEN);
                                goto QuitRcpt;
                            }

                            *ptr2 = temp;
                            if (!Orcpt) {
                                ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                            MSG451INTERNALERR_LEN);
                                goto QuitRcpt;
                            }
                            break;

                        default:
                            ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                        MSG501PARAMERROR_LEN);
                            goto QuitRcpt;
                            break;
                        }
                        ptr2 = strchr (ptr2, ' ');
                        if (ptr2)
                            while (isspace (*ptr2))
                                ptr2++;
                    }

                    if (!GotFlags)
                        Client->Flags |= DSN_HEADER | DSN_FAILURE;

                    ReplyInt = strlen (name);
                    if (ReplyInt > MAXEMAILNAMESIZE) {
                        ConnWrite (Client->client.conn, MSG501RECIPNO, MSG501RECIPNO_LEN);
                        break;
                    }
                    ptr = strchr (name, '@');
                    if (!ptr && ReplyInt > MAX_USERPART_LEN) {
                        ConnWrite (Client->client.conn, MSG501RECIPNO, MSG501RECIPNO_LEN);
                        break;
                    }
                    else if (ptr && ((ptr - name) > MAX_USERPART_LEN)) {
                        ConnWrite (Client->client.conn, MSG501RECIPNO, MSG501RECIPNO_LEN);
                        break;
                    }

                    if ((ReplyInt = RewriteAddress (Client->nmap.conn, name, To, sizeof (To))) == MAIL_BOGUS) {
                        ConnWrite (Client->client.conn, MSG501RECIPNO, MSG501RECIPNO_LEN);
                    }
                    else {
                        switch (ReplyInt) {
                        case MAIL_REMOTE:{
                                if (!IsAuthed) {
                                    Log(LOG_INFO, "Recipient %s by host %s blocked",
                                        name, LOGIP(Client->client.conn->socketAddress));
                                    ConnWrite (Client->client.conn, 
                                        MSG571SPAMBLOCK,
                                        MSG571SPAMBLOCK_LEN);
                                    goto QuitRcpt;
                                }
                                XplRWReadLockAcquire (&ConfigLock);
                                if (Client->RecipCount >= SMTP.max_recips) {
                                    XplRWReadLockRelease (&ConfigLock);
                                    Log(LOG_INFO, "Recipient limit from %s at host %s to user %s",
                                        Client->AuthFrom? 
                                            (char *) Client->AuthFrom : "",
                                        LOGIP(Client->client.conn->socketAddress),
                                        To);
                                    ConnWrite (Client->client.conn, MSG550TOOMANY,
                                                MSG550TOOMANY_LEN);
                                    goto QuitRcpt;
                                }
                                XplRWReadLockRelease (&ConfigLock);
                                snprintf (Answer, sizeof (Answer),
                                          "QSTOR TO %s %s %lu\r\n", To,
                                          Orcpt ? Orcpt : To,
                                          (unsigned long) (Client->
                                                           Flags &
                                                           DSN_FLAGS));
                                Log(LOG_INFO, "Message from %s at host %s relayed to %s",
                                    Client->AuthFrom? 
                                        (char *)Client->AuthFrom : "",
                                    LOGIP(Client->client.conn->socketAddress),
                                    To);
                                break;
                            }

                        case MAIL_RELAY:{
                                Log(LOG_INFO, "Message from %s at host %s relayed to %s",
                                    Client->AuthFrom?
                                        (char *)Client->AuthFrom : "",
                                    LOGIP(Client->client.conn->socketAddress),
                                    To);
                                snprintf (Answer, sizeof (Answer),
                                          "QSTOR TO %s %s %lu\r\n", To,
                                          Orcpt ? Orcpt : To,
                                          (unsigned long) (Client->
                                                           Flags &
                                                           DSN_FLAGS));
                                break;
                            }

                        case MAIL_LOCAL:{
                                XplRWReadLockAcquire (&ConfigLock);
                                if ((NullSender == FALSE) || (Client->RecipCount < SMTP.max_null_sender)) {
                                    if (!SMTP.verify_address) {
                                        XplRWReadLockRelease (&ConfigLock);
                                        snprintf (Answer, sizeof (Answer),
                                                  "QSTOR LOCAL %s %s %lu\r\n",
                                                  To, Orcpt ? Orcpt : To,
                                                  (unsigned long) (Client->Flags & DSN_FLAGS));
                                    } else {
                                        XplRWReadLockRelease (&ConfigLock);
                                        if (MsgAuthFindUser(To) == 0) {
                                            snprintf (Answer, sizeof (Answer),
                                                      "QSTOR LOCAL %s %s %lu\r\n",
                                                      To, Orcpt ? Orcpt : To,
                                                      (unsigned long) (Client->Flags & DSN_FLAGS));
                                        } else {
                                            ConnWrite (Client->client.conn, MSG550NOTFOUND, MSG550NOTFOUND_LEN);
                                            goto QuitRcpt;
                                        }
                                    }

                                }
                                else {
                                    /* TODO - Inform the connection manager that this address should be blocked */

                                    XplRWReadLockRelease (&ConfigLock);

                                    TooManyNullSenderRecips = TRUE;
                                    XplDelay (250);

                                    ConnWrite (Client->client.conn, MSG550TOOMANY,
                                                MSG550TOOMANY_LEN);

                                    Client->RecipCount++;
                                    goto QuitRcpt;
                                }

                                break;
                            }
                        }
                        NMAPSendCommand (Client->nmap.conn, Answer, strlen (Answer));
                        if (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                            ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            if (Orcpt)
                                MemFree (Orcpt);
                            return (EndClientConnection (Client));
                        }
                        Client->State = STATE_TO;
                        Client->RecipCount++;
                        ConnWrite (Client->client.conn, MSG250RECIPOK, MSG250RECIPOK_LEN);
                    }
                  QuitRcpt:
                    if (Orcpt)
                        MemFree (Orcpt);
                }
                break;

            default:
                ConnWrite (Client->client.conn, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                break;
            }
            break;

        case 'M':{             /* MAIL FROM */
                unsigned long size = 0;
                unsigned char temp, *name;
                unsigned char *Envid = NULL;
                unsigned char *more;

                if (SMTP.require_auth && !IsTrusted) {
                    ConnWrite (Client->client.conn, MSG553SPAMBLOCK, MSG553SPAMBLOCK_LEN);
                    break;
                }

                /* rfc 2821 */
                if (!Client->State || Client->State >= STATE_FROM) {
                    ConnWrite (Client->client.conn, MSG503BADORDER, MSG503BADORDER_LEN);
                    break;
                }

                if ((ptr = strchr (Client->Command, ':')) == NULL) {
                    ConnWrite (Client->client.conn, MSG501SYNTAX, MSG501SYNTAX_LEN);
                    break;
                }

                ptr++;
                while (isspace (*ptr)) {
                    ptr++;
                }

                if (*ptr == '<') {
                    ptr++;
                    name = ptr;
                    do {
                        if ((*ptr != '\0') && !isspace (*ptr)) {
                            if (*ptr != '>') {
                                ptr++;
                                continue;
                            }

                            /* we found the end */
                            *ptr = '\0';
                            more = ptr + 1;

                            if (ptr > name) {
                                break;
                            }
                            else {
                                NullSender = TRUE;
                                break;
                            }
                        }
                        /* illegal address */
                        name = NULL;
                        break;

                    } while (TRUE);
                }
                else {
                    name = ptr;
                    do {
                        if ((*ptr != '\0') && !isspace (*ptr)) {
                            ptr++;
                            continue;

                        }

                        /* we found the end */
                        if (*ptr) {
                            *ptr = '\0';
                            more = ptr + 1;
                        }
                        else {
                            more = NULL;
                        }

                        if (ptr > name) {
                            break;
                        }
                        else {
                            NullSender = TRUE;
                            break;
                        }
                    } while (TRUE);
                }

                if (name) {
                    ;
                }
                else {
                    ConnWrite (Client->client.conn, MSG501SYNTAX, MSG501SYNTAX_LEN);
                    break;
                }

                if (more) {
                    do {
                        if (isspace (*more)) {
                            more++;
                            continue;
                        }

                        break;

                    } while (TRUE);
                }

#if 0
                /* We're abusing size, so we don't need another stack variable */
                /* Spam block */
                if (SMTP.block_rts && (name[0] == '\0')) {
                    XplRWReadLockAcquire (&ConfigLock);
                    time (&size);

                    if (LastBounce >= size - BounceInterval) {
                        /* Should make a decision */
                        LastBounce = size;
                        BounceCount++;
                        if (BounceCount > MaxBounceCount) {
                            /* We don't want the bounce, probably spam */
                            XplRWReadLockRelease (&ConfigLock);
                            SendClient (Client, MSG572SPAMBOUNCEBLOCK,
                                        MSG572SPAMBOUNCEBLOCK_LEN);
                            break;
                        }
                    }
                    else {
                        LastBounce = size;
                        BounceCount = 0;
                    }
                    size = 0;
                    XplRWReadLockRelease (&ConfigLock);
                }
#endif


                while (more && *more != '\0') {
                    switch (toupper (*more)) {
                    case 'A':{ /* AUTH */
                            while ((*more) && !isspace (*more)) {
                                more++;
                            }
                            break;
                        }

                    case 'B':  /* BODY */
                        Client->MsgFlags &= ~MSG_FLAG_ENCODING_NONE;

                        switch (more[5]) {
                        case '7':      /* 7BIT */
                            Client->MsgFlags |= MSG_FLAG_ENCODING_7BIT;
                            break;
                        case '8':      /* 8BITMIME */
                            Client->MsgFlags |= MSG_FLAG_ENCODING_8BITM;
                            break;
                        case 'B':
                        case 'b':      /* BINARYMIME */
                            Client->MsgFlags |= MSG_FLAG_ENCODING_BINM;
                            break;
                        default:
                            ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                        MSG501PARAMERROR_LEN);
                            goto QuitMail;
                            break;
                        }
                        break;

                    case 'R':  /* RET */
                        switch (toupper (more[4])) {
                        case 'F':      /* FULL */
                            Client->Flags |= DSN_HEADER | DSN_BODY;
                            break;
                        case 'H':      /* HDRS */
                            Client->Flags |= DSN_HEADER;
                            break;
                        default:
                            ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                        MSG501PARAMERROR_LEN);
                            goto QuitMail;
                            break;
                        }
                        break;

                    case 'E':  /* ENVID */
                        ptr = strchr (more, '=');
                        if (!ptr) {
                            ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                        MSG501PARAMERROR_LEN);
                            goto QuitMail;
                            break;
                        }
                        ptr++;
                        more = ptr;
                        while (!isspace (*more) && *more != '\0')
                            more++;
                        temp = *more;
                        *more = '\0';
                        Envid = MemStrdup (ptr);
                        *more = temp;
                        if (!Envid) {
                            ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            goto QuitMail;
                        }
                        break;

                    case 'S':  /* SIZE */
                        ptr = strchr (more, '=');
                        if (!ptr) {
                            ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                        MSG501PARAMERROR_LEN);
                            goto QuitMail;
                            break;
                        }
                        ptr++;
                        size = atol (ptr);
                        XplRWReadLockAcquire (&ConfigLock);
                        if (SMTP.message_limit > 0 && size > SMTP.message_limit) {
                            XplRWReadLockRelease (&ConfigLock);
                            ConnWrite (Client->client.conn, MSG552MSGTOOBIG,
                                        MSG552MSGTOOBIG_LEN);
                            goto QuitMail;
                        }
                        XplRWReadLockRelease (&ConfigLock);
                        break;

                    default:
                        ConnWrite (Client->client.conn, MSG501PARAMERROR,
                                    MSG501PARAMERROR_LEN);
                        goto QuitMail;
                        break;
                    }
                    more = strchr (more, ' ');
                    if (more)
                        while (isspace (*more))
                            more++;
                }

                ReplyInt = strlen (name);
                if (ReplyInt > MAXEMAILNAMESIZE) {
                    ConnWrite (Client->client.conn, MSG501GOAWAY, MSG501GOAWAY_LEN);
                    if (Envid)
                        MemFree (Envid);
                    break;
                }

                ptr = strchr (name, '@');
                if (!ptr && ReplyInt > MAX_USERPART_LEN) {
                    if (Envid)
                        MemFree (Envid);
                    ConnWrite (Client->client.conn, MSG501GOAWAY, MSG501GOAWAY_LEN);
                    break;
                }
                else if (ptr && ((ptr - name) > MAX_USERPART_LEN)) {
                    if (Envid)
                        MemFree (Envid);
                    ConnWrite (Client->client.conn, MSG501GOAWAY, MSG501GOAWAY_LEN);
                    break;
                }


                if (size > 0) {
                    NMAPSendCommand (Client->nmap.conn, "QDSPC\r\n", 7);
                    if ((ReplyInt = NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE)) != 1000) {
                        ConnWrite (Client->client.conn, MSG451INTERNALERR, MSG451INTERNALERR_LEN);
                        if (Envid) {
                            MemFree (Envid);
                        }
                        return (EndClientConnection (Client));
                    }
                    if ((unsigned long) atol (Reply) < size) {
                        ConnWrite (Client->client.conn, MSG452NOSPACE, MSG452NOSPACE_LEN);
                        goto QuitMail;
                    }
                }
                NMAPSendCommand (Client->nmap.conn, "QCREA\r\n", 7);
                if ((ReplyInt = NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE)) != 1000) {
                    if (ReplyInt == 5221) {
                        ConnWrite (Client->client.conn, MSG452NOSPACE, MSG452NOSPACE_LEN);
                    }
                    else {
                        ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                    MSG451INTERNALERR_LEN);
                    }
                    if (Envid)
                        MemFree (Envid);
                    return (EndClientConnection (Client));
                }

                if (Client->From != NULL) {
                    MemFree (Client->From);
                }
                Client->From = MemStrdup (name);

                snprintf (Answer, sizeof (Answer), "QSTOR FROM %s %s %s\r\n",
                          name[0] != '\0' ? (char *) name : "-",
                          Client->AuthFrom ? (char *) Client->
                          AuthFrom : (char *) "-",
                          Envid ? (char *) Envid : (char *) "");
                NMAPSendCommand (Client->nmap.conn, Answer, strlen (Answer));
                if (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                    if (Envid) {
                        MemFree (Envid);
                    }

                    ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                MSG451INTERNALERR_LEN);
                    return (EndClientConnection (Client));
                }

                snprintf (Answer, sizeof (Answer), "QSTOR ADDRESS %u\r\n",
                          XplHostToLittle (Client->client.conn->socketAddress.sin_addr.s_addr));
                NMAPSendCommand (Client->nmap.conn, Answer, strlen (Answer));
                if (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                    if (Envid) {
                        MemFree (Envid);
                    }

                    ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                MSG451INTERNALERR_LEN);
                    return (EndClientConnection (Client));
                }

                if (!(Client->Flags & DSN_HEADER)
                    && !(Client->Flags & DSN_BODY))
                    Client->Flags |= DSN_HEADER;
                Client->State = STATE_FROM;
                ConnWrite (Client->client.conn, MSG250SENDEROK, MSG250SENDEROK_LEN);
              QuitMail:
                if (Envid) {
                    MemFree (Envid);
                }
            }
            break;

        case 'D':{             /* DATA */
                unsigned char TimeBuf[80];
                unsigned long BReceived = 0;
                unsigned char WithProtocol[8]; /* ESMTPSA */
#ifdef USE_HOPCOUNT_DETECTION
                long HopCount = 0;
#endif

                if (Client->State < STATE_TO && Client->State != STATE_BDAT) {
                    ConnWrite (Client->client.conn, MSG503BADORDER, MSG503BADORDER_LEN);
                    break;
                }

                if (Client->MsgFlags & MSG_FLAG_ENCODING_NONE) {
                    Client->MsgFlags &= ~MSG_FLAG_ENCODING_NONE;
                    Client->MsgFlags |= MSG_FLAG_ENCODING_7BIT;
                }

                Client->MsgFlags |= MSG_FLAG_SOURCE_EXTERNAL;
                snprintf (Answer, sizeof (Answer), "QSTOR FLAGS %d\r\n",
                          Client->MsgFlags);
                NMAPSendCommand (Client->nmap.conn, Answer, strlen (Answer));
                if (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                    ConnWrite (Client->client.conn, MSG451INTERNALERR, MSG451INTERNALERR_LEN);
                    return (EndClientConnection (Client));
                }

                NMAPSendCommand (Client->nmap.conn, "QSTOR MESSAGE\r\n", 15);

                ConnWrite (Client->client.conn, MSG354DATA, MSG354DATA_LEN);
                ConnFlush (Client->client.conn);

                MsgGetRFC822Date(-1, 0, TimeBuf);
                /* rfc 3848 */
                if (Client->IsEHLO) {
                    snprintf(WithProtocol, 8, "ESMTP%s%s",
                    Client->client.conn->ssl.enable ? "S" : "",
                    Client->AuthFrom ? "A" : "");
                } else {
                    snprintf(WithProtocol, 5, "SMTP");
                }
                snprintf(Answer, sizeof(Answer), "Received: from %s (%d.%d.%d.%d) by %s\r\n\twith %sSMTP%s%s (bongosmtp Agent); %s\r\n",
                    Client->RemoteHost,
                    Client->client.conn->socketAddress.sin_addr.s_net,
                    Client->client.conn->socketAddress.sin_addr.s_host,
                    Client->client.conn->socketAddress.sin_addr.s_lh,
                    Client->client.conn->socketAddress.sin_addr.s_impno,
                    BongoGlobals.hostname,
                    (Client->IsEHLO) ? "E" : "",
                    (Client->client.conn->ssl.enable) ? "S" : "",
                    (Client->AuthFrom) ? "A" : "",
                    TimeBuf);
                NMAPSendCommand(Client->nmap.conn, Answer, strlen(Answer));

                /* read all the data to the nmap connection.  this function
                 * takes into account the single . terminating the connection */
                if (ConnReadToConnUntilEOS(Client->client.conn, Client->nmap.conn) < 0) {
                    ConnWrite(Client->client.conn, MSG451INTERNALERR, MSG451INTERNALERR_LEN);
                    return(EndClientConnection(Client));
                }

                XplRWReadLockAcquire (&ConfigLock);
                if (SMTP.message_limit > 0 && BReceived > SMTP.message_limit) {     /* Message over limit */
                    XplRWReadLockRelease (&ConfigLock);
                    if ((NMAPSendCommand (Client->nmap.conn, "QABRT\r\n", 7) < 0) ||
                        NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                        ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                    MSG451INTERNALERR_LEN);
                        return (EndClientConnection (Client));
                    }
                    ConnWrite (Client->client.conn, MSG552MSGTOOBIG, MSG552MSGTOOBIG_LEN);
                }
                else {
                    XplRWReadLockRelease (&ConfigLock);

                    if ((NullSender == FALSE)
                        || (Client->RecipCount < SMTP.max_null_sender)) {
                        if (NMAPSendCommand (Client->nmap.conn, "QRUN\r\n", 6)<0) {
                            ConnWrite (Client->client.conn, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            return (EndClientConnection (Client));
                        }

                        Log(LOG_INFO,
                            "Message received from %s at host %s (%d)",
                            Client->From? (char *)Client->From : "",
                            inet_ntoa(Client->client.conn->socketAddress.sin_addr),
                            BReceived);

                        if (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                            ConnWrite (Client->client.conn, MSG451INTERNALERR, MSG451INTERNALERR_LEN);
                            return (EndClientConnection (Client));
                        }

                        ConnWrite (Client->client.conn, MSG250OK, MSG250OK_LEN);

                        NullSender = FALSE;
                        TooManyNullSenderRecips = FALSE;

                        Client->State = STATE_HELO;
                        Client->RecipCount = 0;

                        break;
                    }

                    Log(LOG_NOTICE, "Added %s at host %s to block list (count: %d)",
                        Client->From? (char *) Client->From : "",
                        LOGIP(Client->client.conn->socketAddress), 
                        Client->RecipCount);

                    /*      We are going to send him away!  */
                    ConnWrite (Client->client.conn, MSG550TOOMANY, MSG550TOOMANY_LEN);

                    /*      EndClientConnection will send QABRT and QUIT to the NMAP server.        */
                    return (EndClientConnection (Client));
                }

                break;
            }
        case 'Q':              /* QUIT */
            if (Client->State != STATE_DATA) {
                if (Client->State != STATE_FRESH) {
                    NMAPSendCommand (Client->nmap.conn, "QABRT\r\n", 7);
                    if (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                        ConnWrite (Client->client.conn, MSG451INTERNALERR, MSG451INTERNALERR_LEN);
                        return (EndClientConnection (Client));
                    }
                }
            }
            snprintf (Answer, sizeof (Answer), "221 %s %s\r\n", BongoGlobals.hostname, MSG221QUIT);
            ConnWrite (Client->client.conn, Answer, strlen (Answer));
            return (EndClientConnection (Client));
            break;

        case 'N':              /* NOOP */
            ConnWrite (Client->client.conn, MSG250OK, MSG250OK_LEN);
            break;

        case 'E':{
                switch (toupper (Client->Command[1])) {
                case 'H':{     /* EHLO */
                        if (Client->State == STATE_FRESH) {
                            XplRWReadLockAcquire (&ConfigLock);
                            if (SMTP.message_limit > 0) {
                                snprintf (Answer, sizeof (Answer),
                                          "250-%s %s\r\n%s%s%s%s %lu\r\n",
                                          BongoGlobals.hostname, MSG250HELLO,
                                          SMTP.accept_etrn ? MSG250ETRN : "",
                                          (SMTP.allow_client_ssl
                                           && !Client->client.conn->ssl.enable) ? MSG250TLS : "",
                                          (AllowAuth ==
                                           TRUE) ? MSG250AUTH : "",
                                          MSG250EHLO, SMTP.message_limit);
                            }
                            else {
                                snprintf (Answer, sizeof (Answer),
                                          "250-%s %s\r\n%s%s%s%s\r\n",
                                          BongoGlobals.hostname, MSG250HELLO,
                                          SMTP.accept_etrn ? MSG250ETRN : "",
                                          (SMTP.allow_client_ssl
                                           && !Client->client.conn->ssl.enable) ? MSG250TLS : "",
                                          (AllowAuth ==
                                           TRUE) ? MSG250AUTH : "",
                                          MSG250EHLO);
                            }
                            XplRWReadLockRelease (&ConfigLock);
                            ConnWrite (Client->client.conn, Answer, strlen (Answer));
                            strncpy (Client->RemoteHost, Client->Command + 5,
                                     sizeof (Client->RemoteHost));
                            Client->State = STATE_HELO;

                            NullSender = FALSE;
                            TooManyNullSenderRecips = FALSE;
                            Client->IsEHLO = TRUE;
                        }
                        else {
                            ConnWrite (Client->client.conn, MSG503BADORDER,
                                        MSG503BADORDER_LEN);
                        }
                    }
                    break;

                case 'T':{     /* ETRN */
                        count = 0;
                        XplRWReadLockAcquire (&ConfigLock);
                        if (SMTP.accept_etrn) {
                            XplRWReadLockRelease (&ConfigLock);
                            ReplyInt =
                                snprintf (Answer, sizeof (Answer),
                                          "QSRCH DOMAIN %s\r\n",
                                          Client->Command + 5);
                            NMAPSendCommand (Client->nmap.conn, Answer, ReplyInt);
                            while (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) == 2001) {
                                ReplyInt = snprintf (Answer, sizeof (Answer), "QRUN %s\r\n", Reply);
                                NMAPSendCommand (Client->nmap.conn, Answer, ReplyInt);
                                count++;
                            }
                            for (ReplyInt = 0; ReplyInt < count; ReplyInt++) {
                                NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE);
                            }
                            if (count > 0) {
                                ConnWrite (Client->client.conn, MSG252QUEUESTARTED, MSG252QUEUESTARTED_LEN);
                            }
                            else {
                                ConnWrite (Client->client.conn, MSG251NOMESSAGES, MSG251NOMESSAGES_LEN);
                            }
                        }
                        else {
                            XplRWReadLockRelease (&ConfigLock);
                            ConnWrite (Client->client.conn, MSG502DISABLED, MSG502DISABLED_LEN);
                        }
                        break;
                    }

                case 'X':{     /* EXPN */
                        XplRWReadLockAcquire (&ConfigLock);
                        if (SMTP.allow_expn) {
                            BOOL Found = FALSE;

                            XplRWReadLockRelease (&ConfigLock);
                            snprintf (Answer, sizeof (Answer), "VRFY %s\r\n",
                                      Client->Command + 5);
                            NMAPSendCommand (Client->nmap.conn, Answer, strlen (Answer));
                            while (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                                Found = TRUE;
                                ConnWrite (Client->client.conn, "250-", 4);
                                ConnWrite (Client->client.conn, Reply, strlen (Reply));
                                ConnWrite (Client->client.conn, "\r\n", 2);
                            }

                            if (Found) {
                                ConnWrite (Client->client.conn, MSG250OK, MSG250OK_LEN);
                            }
                            else {
                                ConnWrite (Client->client.conn, MSG550NOTFOUND,
                                            MSG550NOTFOUND_LEN);
                            }
                        }
                        else {
                            XplRWReadLockRelease (&ConfigLock);
                            ConnWrite (Client->client.conn, MSG502DISABLED,
                                        MSG502DISABLED_LEN);
                        }
                        break;
                    }
                default:
                    ConnWrite (Client->client.conn, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                    break;
                }
                break;
            }

        case 'V':{             /* VRFY */
                XplRWReadLockAcquire (&ConfigLock);
                if (SMTP.allow_vrfy) {
                    BOOL Found = FALSE;

                    XplRWReadLockRelease (&ConfigLock);
                    snprintf (Answer, sizeof (Answer), "VRFY %s\r\n",
                              Client->Command + 5);
                    NMAPSendCommand (Client->nmap.conn, Answer, strlen (Answer));
                    while (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                        Found = TRUE;
                        ConnWrite (Client->client.conn, "250-", 4);
                        ConnWrite (Client->client.conn, Reply, strlen (Reply));
                        ConnWrite (Client->client.conn, "\r\n", 2);
                    }

                    if (Found) {
                        ConnWrite (Client->client.conn, MSG250OK, MSG250OK_LEN);
                    }
                    else {
                        ConnWrite (Client->client.conn, MSG550NOTFOUND,
                                    MSG550NOTFOUND_LEN);
                    }
                }
                else {
                    XplRWReadLockRelease (&ConfigLock);
                    ConnWrite (Client->client.conn, MSG502DISABLED, MSG502DISABLED_LEN);
                }
                break;
            }

        default:
            ConnWrite (Client->client.conn, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
            break;
        }
        ConnFlush (Client->client.conn);
    }
    return (TRUE);
}

#define DELIVER_ERROR(_e)		{			\
	unsigned int i;										\
	for(i = 0; i < RecipCount; i++) {	\
		Recips[i].Result = _e;				\
	}												\
	return(_e);									\
}

#define DELIVER_ERROR_NO_RETURN(_e)	{	\
	unsigned int i;										\
	for(i = 0; i < RecipCount; i++) {	\
		Recips[i].Result = _e;				\
	}												\
}

#define HandleFailure(CheckFor)	if (status!=CheckFor) {									\
													if ((int)strlen(Reply) > ResultLen) {				\
														strncpy(Result, Reply, ResultLen-1);	\
														Result[ResultLen-1]='\0';					\
													} else {												\
														strcpy(Result, Reply);						\
													}														\
													if (Exiting || (status/100==4)) {			\
														DELIVER_ERROR(DELIVER_TRY_LATER);		\
													} else {												\
														DELIVER_ERROR(DELIVER_FAILURE);			\
													}														\
												}


static int
GetEHLO (ConnectionStruct * Client, unsigned long *Extensions, long *Size)
{
    BOOL MultiLine;
    int Result;
    unsigned char *ptr;

    *Size = 0;

    do {
        ConnReadToAllocatedBuffer(Client->remotesmtp.conn, &(Client->remotesmtp.buffer), &(Client->remotesmtp.buflen));
        if (Client->remotesmtp.buffer[3] == '-') {
            MultiLine = TRUE;
            Client->remotesmtp.buffer[3] = ' ';
        } else {
            MultiLine = FALSE;
        }

        if (QuickCmp (Client->remotesmtp.buffer, "250 DSN"))
            *Extensions |= EXT_DSN;
        else if (QuickCmp (Client->remotesmtp.buffer, "250 PIPELINING"))
            *Extensions |= EXT_PIPELINING;
        else if (QuickCmp (Client->remotesmtp.buffer, "250 8BITMIME"))
            *Extensions |= EXT_8BITMIME;
        else if (QuickCmp (Client->remotesmtp.buffer, "250 AUTH=LOGIN"))
            *Extensions |= EXT_AUTH_LOGIN;
        else if (QuickCmp (Client->remotesmtp.buffer, "250 CHUNKING"))
            *Extensions |= EXT_CHUNKING;
        else if (QuickCmp (Client->remotesmtp.buffer, "250 BINARYMIME"))
            *Extensions |= EXT_BINARYMIME;
        else if (QuickCmp (Client->remotesmtp.buffer, "250 ETRN"))
            *Extensions |= EXT_ETRN;
        else if (QuickCmp (Client->remotesmtp.buffer, "250 STARTTLS"))
            *Extensions |= EXT_TLS;
        else if (QuickNCmp (Client->remotesmtp.buffer, "250 SIZE", 8)) {
            *Extensions |= EXT_SIZE;
            if (Client->remotesmtp.buffer[8] == ' ') {
                *Size = atol (Client->remotesmtp.buffer + 9);
            }
        }
    } while (MultiLine);

    ptr = strchr (Client->remotesmtp.buffer, ' ');
    if (!ptr) {
        return (-1);
    }
    *ptr = '\0';
    Result = atoi (Client->remotesmtp.buffer);
    return (Result);
}


static int
GetAnswer (ConnectionStruct * Client, unsigned char *Reply, int ReplyLen)
{
    BOOL MultiLine;
    int Result;
    unsigned char *ptr;

    do {
        ConnReadToAllocatedBuffer(Client->remotesmtp.conn, &(Client->remotesmtp.buffer), &(Client->remotesmtp.buflen));
        if (Client->remotesmtp.buffer[3] == '-') {
            MultiLine = TRUE;
        }
        else {
            MultiLine = FALSE;
        }
    } while (MultiLine);
    ptr = strchr (Client->remotesmtp.buffer, ' ');
    if (!ptr) {
        return (-1);
    }
    *ptr = '\0';
    Result = atoi (Client->remotesmtp.buffer);
    memmove (Reply, ptr + 1, min(strlen(ptr + 1) + 1, (unsigned int)ReplyLen-1));
    return (Result);
}

static BOOL
SendServerEscaped (ConnectionStruct * Client, unsigned char *Data,
                   unsigned long Len, BOOL * EscapedState)
{
    unsigned char *ptr = Data;
    unsigned long Pos, EndPos;

    EndPos = Len - 1;

    if (*EscapedState) {
        if (Data[0] == '.') {
            if (ConnWrite(Client->remotesmtp.conn, ".", 1) < 1) {
                return (FALSE);
            }
        }
    }
    *EscapedState = FALSE;

    Pos = 0;
    while (Pos < EndPos) {
        if (Data[Pos] != '\n') {
            Pos++;
        }
        else {
            if (Data[Pos + 1] == '.') {
                if (ConnWrite(Client->remotesmtp.conn, ptr, Pos - (ptr - Data) + 1) < 1) {
                    return (FALSE);
                }
                if (ConnWrite(Client->remotesmtp.conn, ".", 1) < 1) {
                    return (FALSE);
                }
                ptr = Data + Pos + 1;
            }
            Pos++;
        }
    }
    if (ConnWrite(Client->remotesmtp.conn, ptr, Pos - (ptr - Data) + 1) < 1) {
        return (FALSE);
    }
    if (Data[EndPos] == '\n') {
        *EscapedState = TRUE;
    }
    return (TRUE);
}

static int
DeliverSMTPMessage (ConnectionStruct * Client, unsigned char *Sender,
                    RecipStruct * Recips, unsigned int RecipCount,
                    unsigned int MsgFlags, unsigned char *Result,
                    int ResultLen)
{
    unsigned long Extensions = 0;
    unsigned char Answer[1026];
    unsigned char Reply[1024];
    unsigned char *ptr, *EnvID = NULL;
    int status;
    long Size, len, MessageSize;
    unsigned int i;
    BOOL EscapedState = FALSE;
    unsigned long LastNMAPContact;
    unsigned char TimeBuf[80];
    BOOL ehlo=TRUE;
    BOOL tls=FALSE;

    status = GetAnswer (Client, Reply, sizeof (Reply));
    HandleFailure (220);

    snprintf (Answer, sizeof (Answer), "EHLO %s\r\n", BongoGlobals.hostname);
    if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }
    ConnFlush(Client->remotesmtp.conn);

    status = GetEHLO (Client, &Extensions, &Size);
    if (status != 250) {
        snprintf (Answer, sizeof (Answer), "HELO %s\r\n", BongoGlobals.hostname);
        if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
        ConnFlush(Client->remotesmtp.conn);
        status = GetAnswer (Client, Reply, sizeof (Reply));
        HandleFailure (250);
        ehlo=FALSE;
    }
    else {
        /* The other server supports ESMTP */

        // SSL delivery disabled per bug #9536
        if (0 && SMTP.allow_client_ssl && (Extensions & EXT_TLS)) {
            snprintf (Answer, sizeof (Answer), "STARTTLS\r\n");
            if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
            ConnFlush(Client->remotesmtp.conn);
            ConnReadAnswer(Client->remotesmtp.conn, Reply, sizeof(Reply));
            if (Reply[0] == '2') {
                if(ConnEncrypt(Client->remotesmtp.conn, SSLContext) < 0) {
                    DELIVER_ERROR (DELIVER_FAILURE);
                }
                status = snprintf (Answer, sizeof (Answer), "EHLO %s\r\n", BongoGlobals.hostname);
                if (ConnWrite(Client->remotesmtp.conn, Answer, status) < 1) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
                ConnFlush(Client->remotesmtp.conn);
                status = GetAnswer (Client, Reply, sizeof (Reply));
                tls=TRUE;
            }
        }

        /* Do a size check */
        if ((Size > 0) && ((unsigned long) Size < Client->Flags)) {     /* Client->Flags holds size of data file */
            sprintf (Result,
                     "The recipient's server does not accept messages larger than %ld bytes. Your message was %ld bytes.",
                     Size, Client->Flags);
            sprintf (Answer, "QUIT\r\n");
            ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer));
            ConnFlush(Client->remotesmtp.conn);
            status = GetAnswer (Client, Reply, sizeof (Reply));
            DELIVER_ERROR (DELIVER_FAILURE);
        }
    }

    /* We abuse reply for the sender string; must be non-destructive; will get reused if multiple recipients */
    if (Extensions & EXT_DSN) {
        strcpy (Reply, Sender);
        ptr = strchr (Reply, ' ');
        if (ptr) {
            *ptr = '\0';
            ptr = strchr (ptr + 1, ' ');
            if (ptr) {
                EnvID = ptr + 1;
                if (!*EnvID)
                    EnvID = NULL;
            }
        }
    }
    else {
        strcpy (Reply, Sender);
        ptr = strchr (Reply, ' ');
        if (ptr) {
            *ptr = '\0';
        }
    }

    if (Reply[0] == '-' && Reply[1] == '\0') {
        Reply[0] = '\0';
    }

    /* Keep this close to the above code; we rely on Reply not being overwritten */
    snprintf (Answer, sizeof (Answer), "MAIL FROM:<%s>", Reply);
    if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }

    if (Extensions & EXT_SIZE) {
        sprintf (Answer, " SIZE=%ld", Client->Flags);
        if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
    }

    if (Extensions & EXT_DSN) {
        if (Recips[0].Flags & DSN_BODY) {
            if (ConnWrite(Client->remotesmtp.conn, " RET=FULL", 9) < 1) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
        }
        else if (Recips[0].Flags & DSN_HEADER) {
            if (ConnWrite(Client->remotesmtp.conn, " RET=HDRS", 9) < 1) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
        }
        if (EnvID) {
            snprintf (Answer, sizeof (Answer), " ENVID=%s", EnvID);
            if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
        }
    }
    /* End of Reply variable protection area */

    if ((Extensions & EXT_8BITMIME) && (MsgFlags & MSG_FLAG_ENCODING_8BITM)) {
        if (ConnWrite(Client->remotesmtp.conn, " BODY=8BITMIME", 14) < 1) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
    }

    if (ConnWrite(Client->remotesmtp.conn, "\r\n", 2) < 1) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }

    if (!(Extensions & EXT_PIPELINING)) {
        if (!ConnFlush(Client->remotesmtp.conn)) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
        status = GetAnswer (Client, Reply, sizeof (Reply));
        HandleFailure (250);
    }

    /* We abuse Size to determine if any recipients are left */
    Size = 0;

    LastNMAPContact = time (NULL);

    /* RCPT TO */
    for (i = 0; i < RecipCount; i++) {
        if (Recips[i].Result != 0) {
            continue;
        }

        /* Some servers slow down responses to RCPT TO to discourage spamming           */
        /* Bad things happen if NMAP times out. Ping NMAP if 10 minutes goes by         */
        if (((i & 0xf) == 0) && ((time (NULL) - LastNMAPContact) > 300)) {
            NMAPSendCommand (Client->client.conn, "NOOP\r\n", 6);

            if (NMAPReadResponse (Client->nmap.conn, Reply, sizeof (Reply), TRUE) != 1000) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            } else {
                LastNMAPContact = time (NULL);
            }
        }

        snprintf (Answer, sizeof (Answer), "RCPT TO:<%s>", Recips[i].To);
        if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }

        if (Extensions & EXT_DSN) {
            if (Recips[i].ORecip) {
                if (XplStrNCaseCmp (Recips[i].ORecip, "rfc822;", 7) == 0) {     /* Add "rfc822;" if not already present */
                    snprintf (Answer, sizeof (Answer), " ORCPT=%s",
                              Recips[i].ORecip);
                }
                else {
                    /* Encode according to RFC 1891 */
                    unsigned char HexTable[] = "0123456789ABCDEF";
                    unsigned char *src;
                    unsigned char *dst;

                    strcpy (Answer, " ORCPT=rfc822;");
                    dst = Answer + 14;
                    src = Recips[i].ORecip;

                    while (*src != '\0') {
                        switch (*src) {
                        case '+':
                        case '=':{
                                *(dst++) = '+';
                                *(dst++) = HexTable[((0xF0 & *src) >> 4)];
                                *(dst++) = HexTable[(0x0F & *src)];
                                break;
                            }

                        default:{
                                *dst = *src;
                                dst++;
                                break;
                            }
                        }
                        src++;
                    }

                    *dst = '\0';
                }
                if (ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer)) < 1) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
            }
            if (Recips[i].Flags & (DSN_SUCCESS | DSN_FAILURE | DSN_TIMEOUT)) {
                BOOL Comma = FALSE;
                if (ConnWrite(Client->remotesmtp.conn, " NOTIFY=", 8) < 1) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
                if (Recips[i].Flags & DSN_SUCCESS) {
                    if (ConnWrite(Client->remotesmtp.conn, "SUCCESS", 7) < 1) {
                        DELIVER_ERROR (DELIVER_TRY_LATER);
                    }
                    Comma = TRUE;
                }
                if (Recips[i].Flags & DSN_TIMEOUT) {
                    if (Comma) {
                        if (ConnWrite(Client->remotesmtp.conn, ",DELAY", 6) < 1) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                    else {
                        if (ConnWrite(Client->remotesmtp.conn, "DELAY", 5) < 1) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                    Comma = TRUE;
                }
                if (Recips[i].Flags & DSN_FAILURE) {
                    if (Comma) {
                        if (ConnWrite(Client->remotesmtp.conn, ",FAILURE", 8) < 1) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                    else {
                        if (ConnWrite(Client->remotesmtp.conn, "FAILURE", 7) < 1) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                }
            }
            else {
                if (ConnWrite(Client->remotesmtp.conn, " NOTIFY=NEVER", 13) < 1) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
            }
        }

        if (ConnWrite(Client->remotesmtp.conn, "\r\n", 2) < 1) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }


        if (!(Extensions & EXT_PIPELINING)) {
            if (!ConnFlush(Client->remotesmtp.conn)) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }

            status = GetAnswer (Client, Reply, sizeof (Reply));
            if (Exiting) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }

            if (status == 251 || status == 250) {
                Log(LOG_INFO, "Message sent from %s to %s (flags: %d, status: %d)",
                    Sender, Recips[i].To, Client->Flags, status);
                Size++;
                continue;
            }
            else if (status / 100 == 4) {
                Log(LOG_INFO, "Message try later from %s to %s Flags %d Status %d",
                        Sender, Recips[i].To, Client->Flags, status);
                Recips[i].Result = DELIVER_TRY_LATER;
                continue;
            }
            else {
                Log(LOG_INFO, "Message failed delivery from %s to %s Flags %d Status %d: %s",
                        Sender, Recips[i].To, Client->Flags, status, Reply);
                if (status == 550) {
                    Recips[i].Result = DELIVER_USER_UNKNOWN;
                    if ((unsigned long) ResultLen > strlen (Reply)) {
                        snprintf (Result, sizeof (Result),
                                  ">>> RCPT TO: <%s>\\r\\n<<< %d %s",
                                  Recips[i].To, status, Reply);
                    }
                    else {
                        strncpy (Result, Reply, ResultLen);
                    }
                    continue;
                }
                else if ((status == 501) && Recips[i].ORecip
                         && (Extensions & EXT_DSN)) {
                    /* workaround for smtp implimentations that do not do ORCPT: properly */
                    Extensions ^= EXT_DSN;
                    i--;
                    continue;
                }
                else {
                    Recips[i].Result = DELIVER_FAILURE;
                }
            }
        }
    }

    /* If Pipelining is turned on, we didn't read the responses (yet) */
    if (Extensions & EXT_PIPELINING) {
        if (!ConnFlush(Client->remotesmtp.conn)) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
        status = GetAnswer (Client, Reply, sizeof (Reply));     /* Pipelined answer from MAIL FROM */
        HandleFailure (250);
        for (i = 0; i < RecipCount; i++) {
            if (Recips[i].Result == 0) {
                status = GetAnswer (Client, Reply, sizeof (Reply));     /* Pipelined answer from RCPT TO */
                if (Exiting) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
                if (status == 251 || status == 250) {
                    Log(LOG_INFO, "Message sent from %s to %s Flags %d Status %d",
                            Sender, Recips[i].To, Client->Flags, status);
                    Size++;
                    continue;
                }
                else if (status / 100 == 4) {
                    Log(LOG_INFO, "Message try later from %s to %s Flags %d Status %d",
                            Sender, Recips[i].To, Client->Flags, status);
                    Recips[i].Result = DELIVER_TRY_LATER;
                    continue;
                }
                else {
                    Log(LOG_INFO, "Message failed delivery from %s to %s Flags %d Status %d",
                            Sender, Recips[i].To, Client->Flags, status);
                    if (status == 550) {
                        Recips[i].Result = DELIVER_USER_UNKNOWN;
                        if ((unsigned long) ResultLen > strlen (Reply)) {       /* FIXME? This is not enough to prevent overwrites, but the buffer is really big... */
                            snprintf (Result, sizeof (Result),
                                      ">>> RCPT TO: <%s>\\r\\n<<< %d %s",
                                      Recips[i].To, status, Reply);
                        }
                        else {
                            strncpy (Result, Reply, ResultLen);
                        }
                        continue;
                    }
                    else {
                        Recips[i].Result = DELIVER_FAILURE;
                    }
                }
            }
        }
    }
    if (Size == 0) {
        FreeInternalConnection(Client->remotesmtp);
        return (DELIVER_FAILURE);
    }

    if (ConnWrite(Client->remotesmtp.conn, "DATA\r\n", 6) < 1) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }
    ConnFlush(Client->remotesmtp.conn);
    status = GetAnswer (Client, Reply, sizeof (Reply));
    HandleFailure (354);

    MsgGetRFC822Date(-1, 0, TimeBuf);
    snprintf(Answer, sizeof(Answer), "Received: from %s (%d.%d.%d.%d) by %s\r\n\twith NMAP (bongosmtp Agent); %s\r\n",
            BongoGlobals.hostname, /* FIXME: this should eventually refelct the queue agent's ip hostname */
            Client->client.conn->socketAddress.sin_addr.s_net,
            Client->client.conn->socketAddress.sin_addr.s_host,
            Client->client.conn->socketAddress.sin_addr.s_lh,
            Client->client.conn->socketAddress.sin_addr.s_impno,
            BongoGlobals.hostname,
            TimeBuf);
    ConnWrite(Client->remotesmtp.conn, Answer, strlen(Answer));

    snprintf(Answer, sizeof(Answer), "QRETR %s MESSAGE\r\n", Client->RemoteHost);
    NMAPSendCommand(Client->client.conn, Answer, strlen(Answer));

    status = NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);

    if (status != 2023) {
        DELIVER_ERROR (DELIVER_FAILURE);
    }
    Size = atol (Reply);
    if (!Size) {
        DELIVER_ERROR (DELIVER_FAILURE);
    }
        
    MessageSize = Size;

    /* Sending message to remote system; must escape any dots on a line */
    while (Size > 0) {
        if ((unsigned int)Size < sizeof (Answer)) {
            len = ConnRead (Client->client.conn, Answer, Size);
        }
        else {
            len = ConnRead (Client->client.conn, Answer, sizeof(Answer));
        }
        if (len > 0) {
            if (!SendServerEscaped (Client, Answer, len, &EscapedState)) {
                DELIVER_ERROR_NO_RETURN (DELIVER_TRY_LATER);
            }
            Size -= len;
        }
        else {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
    }

    /* FIX ME, check for 1000 */
    NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);       /* Get the 1000 OK message for QRETR MESG */

    if (ConnWrite(Client->remotesmtp.conn, "\r\n.\r\n", 5) < 1) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }
    ConnFlush(Client->remotesmtp.conn);

    status = GetAnswer (Client, Reply, sizeof (Reply));
    HandleFailure (250);

    /* Right here we know we've delivered the messages successfully */
    for (i = 0; i < RecipCount; i++) {
        if (Recips[i].Result == 0) {
            Recips[i].Result = DELIVER_SUCCESS;
            if (Extensions & EXT_DSN) {
                Recips[i].Result++;
            }
        }
    }

    if (ConnWrite(Client->remotesmtp.conn, "QUIT\r\n", 6) < 1) {
        status = GetAnswer (Client, Reply, sizeof (Reply));
    }
    ConnFlush(Client->remotesmtp.conn);

    return (DELIVER_SUCCESS);
}

__inline static BOOL
IPAddressIsLocal (struct in_addr IPAddress)
{
    return (((IPAddress.s_addr) == LocalAddress) || ((IPAddress.s_net) == 127)
            || ((IPAddress.s_addr) == 0));
}

static int
DeliverRemoteMessage (ConnectionStruct * Client, unsigned char *Sender,
                      RecipStruct * Recips, unsigned int RecipCount,
                      unsigned long MsgFlags, unsigned char *Result,
                      int ResultLen)
{
    unsigned char Host[MAXEMAILNAMESIZE + 1];
    unsigned char *ptr;
    int status;
    int RetVal;
    XplDns_MxLookup *mx = NULL;
    XplDns_IpList *list = NULL;

    // If we have a relay configured, use that. 
    // Any type of failure is treated as temporary.
    if (SMTP.use_relay) {
        Connection *conn = ConnAlloc(TRUE);
        
        conn->socketAddress.sin_addr.s_addr = inet_addr(SMTP.relay_host);
        conn->socketAddress.sin_family = AF_INET;
        conn->socketAddress.sin_port = htons (25); // TODO: configurable?
        if (conn->socketAddress.sin_addr.s_addr == -1) {
            // TODO: Resolve host? Implies a resolve loop for A recs. tho.
            DELIVER_ERROR_NO_RETURN(DELIVER_TRY_LATER);
        } else {
            Client->remotesmtp.conn = conn;
            RetVal = DeliverSMTPMessage(Client, Sender, Recips, RecipCount,
                                        MsgFlags, Result, ResultLen);
        }
        ConnClose(conn, 1);
        ConnFree(conn);
        if (RetVal != DELIVER_SUCCESS) {
            DELIVER_ERROR(DELIVER_TRY_LATER);
        }
        return DELIVER_SUCCESS;
    }

    // no relay, therefore we want to look for mail exchangers
    ptr = strchr (Recips[0].To, '@');
    if (!ptr) DELIVER_ERROR (DELIVER_BOGUS_NAME);

    ptr++;
    if (ptr[0] == '[') {
        // potential ip address in the host name (e.g. jdoe@[1.1.1.1])
        unsigned char *end;
        end = strchr(ptr, ']');
        if (end) { 
            *end = '\0';
        } else {
            DELIVER_ERROR (DELIVER_BOGUS_NAME);
        }
        // FIXME: assumes our DNS routines will accept IP address.
    }
    strncpy(Host, ptr, MAXEMAILNAMESIZE - 1);

    // Resolve Host using MX routines.
    mx = XplDnsNewMxLookup(Host);
    if (mx->status != XPLDNS_SUCCESS) {
        switch(mx->status) {
            case XPLDNS_SUCCESS:
                break;
            case XPLDNS_TRY_AGAIN:
                RetVal = DELIVER_TIMEOUT;
                break;
            case XPLDNS_NOT_FOUND:
            case XPLDNS_NO_DATA:
                RetVal = DELIVER_HOST_UNKNOWN;
                break;
            default:
            case XPLDNS_DNS_ERROR:
                RetVal = DELIVER_TRY_LATER;
                break;
        }
        DELIVER_ERROR(RetVal);
        goto finish;
    }

    while ((list = XplDnsNextMxLookupIpList(mx)) != NULL) {
        int x;
        
        for (x = 0; x < list->number; x++) {
            // clear return status; not an error
            DELIVER_ERROR_NO_RETURN(0);
            
            if (Exiting) {
                DELIVER_ERROR(DELIVER_TRY_LATER);
                goto finish;
            }
            // FIXME: check for ETRN, or mail being relayed.
            Client->remotesmtp.conn = ConnAlloc(TRUE);
            Client->remotesmtp.conn->socketAddress.sin_addr.s_addr = list->ip_list[x];
            Client->remotesmtp.conn->socketAddress.sin_family = AF_INET;
            Client->remotesmtp.conn->socketAddress.sin_port = htons (25);
            status = ConnConnect(Client->remotesmtp.conn, NULL, 0, NULL);
            if (status < 0) {
                switch (errno) {
                    case ETIMEDOUT:
                        RetVal = DELIVER_TIMEOUT;
                        break;
                    case ECONNREFUSED:
                        RetVal = DELIVER_REFUSED;
                        break;
                    case ENETUNREACH:
                        RetVal = DELIVER_UNREACHABLE;
                        break;
                    default:
                        RetVal = DELIVER_TRY_LATER;
                        break;
                }
                DELIVER_ERROR_NO_RETURN(RetVal);
            } else {
                Result[0] = '\0';
                RetVal = DeliverSMTPMessage(Client, Sender, Recips, RecipCount,
                                            MsgFlags, Result, ResultLen);
            }
            FreeInternalConnection(Client->remotesmtp);
            
            if (RetVal == DELIVER_SUCCESS) {
                goto finish;
            }
            // TODO: Should take into account max MX setting here
            if (RetVal == DELIVER_FAILURE) {
                goto finish;
            }
            if (Exiting) goto finish;
            
            // ping the Queue agent to ensure we don't time out
            NMAPSendCommand (Client->client.conn, "NOOP\r\n", 6);
            NMAPReadResponse (Client->client.conn, Result, ResultLen, TRUE);
            Result[0] = '\0';
        }
    }
    
finish:
    XplDnsFreeMxLookup(mx);

    return (RetVal);
}

int
RewriteAddress(Connection * conn, unsigned char *Source, unsigned char *Target, unsigned int TargetSize)
{
    unsigned char WorkSpace[1024];
    unsigned char *Src, *Dst;
    unsigned int i;
    int RetVal = MAIL_BOGUS;

    /***
	This routine is supposed to rewrite any address into proper format

	several rules exist:
	Bang:		host1!host2!host3!user -> should become user%host3%host2@host1
	Percent:	Check percent address and strip of local hostname if existent,
	replace next % with @
	IP:		check for IP-Addresses peter@[151.155.10.117]
	Address: <dns-addr> (username)

	Clean out spaces etc...
    ***/

    i = strlen (Source);
    if ((TargetSize < i) || (i >= MAXEMAILNAMESIZE)) {
        return (RetVal);
    }

    /** remove illegal chars **/
    Src = Source;
    Dst = WorkSpace;

    while (*Src) {
        if (!isspace (*Src) && *Src != '"') {
            *(Dst++) = *(Src++);
        }
        else {
            Src++;
        }
    }
    *Dst = '\0';

    /* Remove a stray @ at the beginning or end of an address */
    if (WorkSpace[0] == '@') {
        memcpy (WorkSpace, WorkSpace + 1, strlen (WorkSpace));
    }
    else if (WorkSpace[strlen (WorkSpace) - 1] == '@') {
        WorkSpace[strlen (WorkSpace) - 1] = '\0';
    }

    /* Clean address now in Workspace */

    /** Address grab **/
    /* Find the address */
    if ((Src = strchr (WorkSpace, '<')) != NULL) {      /* Address hidden inside <>     */
        Dst = strchr (Src + 1, '>');
        if (!Dst) {
            return (RetVal);
        }
        else {
            *Dst = '\0';
            if (strlen (Src) < TargetSize) {
                strcpy (Target, Src);
            }
            else {
                strncpy (Target, Src, TargetSize - 1);
                Target[TargetSize - 1] = '\0';
            }
        }
    }
    else {
        if (strlen (WorkSpace) < TargetSize) {
            strcpy (Target, WorkSpace);
        }
        else {
            strncpy (Target, WorkSpace, TargetSize - 1);
            Target[TargetSize - 1] = '\0';
        }

        if ((Src = strchr (Target, '(')) != NULL) {     /* Remove name and () */
            Dst = strchr (Src + 1, ')');
            if (Dst) {
                strcpy (Src, Dst + 1);
            }
        }
    }

    /* Clean address now in Target */

    /** Convert Bang - addresses into Domain format **/
    WorkSpace[0] = '\0';
    if ((Src = strrchr (Target, '!')) != NULL) {
        /* Might have % and @ -> make them all @ */
        while ((Dst = strchr (Target, '@')) != NULL) {
            *Dst = '%';
        }
        /* Cut off all @ addresses and remember them (via Dst) */
        if ((Dst = strchr (Target, '%')) != NULL) {
            *Dst = '\0';
        }
        while ((Src = strrchr (Target, '!')) != NULL) {
            strncat (WorkSpace, Src + 1, strlen (Src) + 1);
            strcat (WorkSpace, "%");
            *Src = '\0';
        }
        /* Copy the remembered addresses */
        if (Dst) {
            strcat (WorkSpace, Dst + 1);
            strcat (WorkSpace, "%");
        }
    }
    if ((strlen (WorkSpace) + strlen (Target)) < sizeof (WorkSpace)) {
        strcat (WorkSpace, Target);
    }

    /* Clean address now in WorkSpace */

    /* Do we have a @ in the name? *//* I'll check reverse so I can reuse Src for the next check */
    if ((Src = strrchr (WorkSpace, '@')) == NULL) {
        /* No @, replace the last % with an @ */
        Dst = strrchr (WorkSpace, '%');
        if (!Dst) {             /* No @ or %, simple address, just return */
            if (strlen (WorkSpace) < TargetSize) {
                strcpy (Target, WorkSpace);
            }
            else {
                strncpy (Target, WorkSpace, TargetSize - 1);
                Target[TargetSize - 1] = '\0';
            }
            return (MAIL_LOCAL);
        }
        *Dst = '@';
    }
    else {
        /* Do we have multiple @ in the name, if yes, replace them with % */
        Src--;
        while (Src > WorkSpace) {
            if (*Src == '@')
                *Src = '%';
            Src--;
        }
    }

    /* Clean address now in WorkSpace */
    if (WorkSpace[0] == '\0') {
        return (MAIL_BOGUS);
    }

    /* TODO: i'm not sure this code is functionally equivalient with its replacement due to the address checking */
    {
    	unsigned char addr[1024];
        unsigned char *ptr;

    	NMAPSendCommandF(conn, "ADDRESS RESOLVE %s\r\n", WorkSpace);
    	RetVal = NMAPReadResponse(conn, addr, 1023, FALSE);

        /* find the second space */
        ptr = strchr(addr, ' ');
        if (ptr) {
            ptr++;
            ptr = strchr(ptr, ' ');
            if (ptr) {
                ptr++;
            }
        }

        if (!ptr) {
            /* there was an error getting back the address */
            ptr = WorkSpace;
        }

        if (strlen (ptr) < TargetSize) {
            strcpy (Target, ptr);
        } else {
            strncpy (Target, ptr, TargetSize - 1);
            Target[TargetSize - 1] = '\0';
        }

    	switch (RetVal) {
		case 1000:
			RetVal = MAIL_LOCAL;
			break;
		case 1001:
			RetVal = MAIL_RELAY;
			break;
		case 1002:
			RetVal = MAIL_REMOTE;
			break;
		default:
			RetVal = MAIL_BOGUS;
			break;
    	}
    }

    return (RetVal);
}

#define	QuickCmpToUpper(check,result)		result=check & 0xdf;

static int
RecipCompare (const RecipStruct * Recip1, const RecipStruct * Recip2)
{
    unsigned char *ptr1, *ptr2;
    unsigned char char1, char2;

    if ((!(Recip1->To[0])) || (!(Recip2->To[0]))) {
        return (-1);
    }

    ptr1 = strchr (Recip1->To, '@') + 1;        /* We already made sure the @s exist */
    ptr2 = strchr (Recip2->To, '@') + 1;        /* when we were building the list */
    while (*ptr1 && *ptr2 && *ptr1 != ' ' && *ptr2 != ' ') {
        char1 = *(ptr1++) & 0xdf;
        char2 = *(ptr2++) & 0xdf;
        if (char1 < char2) {
            return (-1);
        }
        else if (char1 > char2) {
            return (1);
        }
    }
    return (0);
}

void
ProcessRemoteEntry (ConnectionStruct * Client, unsigned long Size, int Lines)
{
    unsigned char *ptr, *ptr2;
    unsigned char From[BUFSIZE + 1];
    unsigned char Reply[BUFSIZE + 1];
    unsigned char Result[BUFSIZE + 1];
    BOOL Local = FALSE;
    int rc, i, j = 0, len;
    unsigned long MsgFlags = MSG_FLAG_ENCODING_7BIT;
    RecipStruct *Recips;
    int NumRecips = 0;
    unsigned char *Envelope;
    unsigned char *EnvelopePtr;

    if (!Client)
        return;

    Envelope = MemMalloc(Size+1);
    Recips = MemMalloc(Lines * sizeof(RecipStruct));
    if (!Recips) {
        Log(LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__, (Lines * sizeof (RecipStruct)), __LINE__);
        return;
    }

    if (!Envelope) {
        MemFree(Recips);
        Log(LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__, Size, __LINE__);
        return;
    }

    /* read in the entire stream.  this uses more memory but protects a little better from overflows */
    rc = ConnReadCount(Client->client.conn, (char *)Envelope, Size);
    if ((rc < 0) || ((unsigned long)rc != Size) || (NMAPReadResponse(Client->client.conn, Reply, BUFSIZE, FALSE)) != 6021) {
        MemFree(Envelope);
        MemFree(Recips);
        return;
    }

    EnvelopePtr = Envelope;
    while ((rc = PullLine2(Reply, sizeof(Reply), &EnvelopePtr)) != 6021) {
        switch (Reply[0]) {
        case QUEUE_FROM:{
                rc = snprintf (Result, sizeof (Result), "QMOD RAW %s\r\n", Reply);
                NMAPSendCommand (Client->client.conn, Result, rc);
                strcpy (From, Reply + 1);
            }
            break;

        case QUEUE_FLAGS:{
                MsgFlags = atoi (Reply + 1);
            }
            break;

        case QUEUE_RECIP_REMOTE:{
                /* R<recipient> <original address> <flags> */
                memset(Recips[NumRecips].ORecip, '\0', MAXEMAILNAMESIZE+1);
                Recips[NumRecips].Flags = DSN_FAILURE;
                Recips[NumRecips].Result = 0;

                ptr = strchr (Reply + 1, ' ');
                if (ptr) {
                    *ptr = '\0';
                }

                rc = RewriteAddress (Client->client.conn, Reply + 1, Recips[NumRecips].To, MAXEMAILNAMESIZE);
                if (rc == MAIL_BOGUS) {
                    rc = DELIVER_BOGUS_NAME;
                    Recips[NumRecips].Result = rc;
                }

                /* now go after the original address */
                ptr++;
                ptr2 = strchr(ptr, ' ');
                if (ptr2) {
                    *ptr2 = '\0';
                }

                strncpy(Recips[NumRecips].ORecip, ptr, MAXEMAILNAMESIZE);

                /* lastly go after the flags */
                Recips[NumRecips].Flags = atoi(ptr2+1);

                if (Recips[NumRecips].Result == 0) {
                    switch (rc) {
                    case MAIL_RELAY:
                    case MAIL_REMOTE:{
                            NumRecips++;
                            break;
                        }
                    default:{
                            /* The responses from these commands will be in NMAPs send buffer after our ctrl file */
                            /* we read them after the loop */
                            if (!Local) {
                                rc = snprintf (Reply, sizeof (Reply), "QCOPY %s\r\n", Client->RemoteHost);      /* RemoteHost abused for queue id */
                                NMAPSendCommand (Client->client.conn, Reply, rc);

                                NMAPSendCommand (Client->client.conn, Reply,
                                                snprintf (Reply,
                                                          sizeof (Reply),
                                                          "QSTOR FLAGS %ld\r\n",
                                                          MsgFlags));

                                rc = snprintf (Reply, sizeof (Reply),
                                               "QSTOR FROM %s\r\n", From);
                                NMAPSendCommand (Client->client.conn, Reply, rc);
                                Local = TRUE;
                                j = 0;
                            }
                            if (Recips[NumRecips].ORecip) {
                                rc = snprintf (Reply, sizeof (Reply),
                                               "QSTOR LOCAL %s %s %lu\r\n",
                                               Recips[NumRecips].To,
                                               Recips[NumRecips].ORecip,
                                               Recips[NumRecips].Flags);
                            }
                            else {
                                rc = snprintf (Reply, sizeof (Reply),
                                               "QSTOR LOCAL %s %s %lu\r\n",
                                               Recips[NumRecips].To,
                                               Recips[NumRecips].To,
                                               Recips[NumRecips].Flags);
                            }
                            NMAPSendCommand (Client->client.conn, Reply, rc);
                            j++;
                            break;
                        }
                    }
                }
                else {
                    NumRecips++;
                }
            }
            break;

        default:{
                rc = snprintf (Result, sizeof (Result), "QMOD RAW %s\r\n", Reply);
                NMAPSendCommand (Client->client.conn, Result, rc);
            }
            break;
        }
    }
    if (Local) {
        NMAPSendCommand (Client->client.conn, "QRUN\r\n", 6);
        NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);   /* QCOPY */
        NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);   /* QSTOR FLAGS */
        NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);   /* QSTOR FROM */
        for (i = 0; i < j; i++) {
            NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);       /* QSTOR LOCALS */
        }
        NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);   /* QRUN */
    }

    qsort (Recips, NumRecips, sizeof (RecipStruct), (void *) RecipCompare);

    i = 0;
    while (i < NumRecips) {
        rc = 1;
        while (i + rc < NumRecips && RecipCompare (&Recips[i], &Recips[i + rc]) == 0) {
            rc++;
        }
        Result[0] = '\0';

        DeliverRemoteMessage (Client, From, &Recips[i], rc, MsgFlags, Result, BUFSIZE);

        for (j = i; j < (i + rc); j++) {
            switch (Recips[j].Result) {
            case DELIVER_SUCCESS:{
                    if (Recips[j].Flags & DSN_SUCCESS) {
                        len = snprintf (Reply, sizeof (Reply),
                                      "QRTS %s %s %lu %d\r\n", Recips[j].To,
                                      Recips[j].ORecip, Recips[j].Flags,
                                      DELIVER_SUCCESS);
                        NMAPSendCommand (Client->client.conn, Reply, len);
                    }
                }
                break;

            case DELIVER_TIMEOUT:
            case DELIVER_REFUSED:
            case DELIVER_UNREACHABLE:
            case DELIVER_TRY_LATER:{
                    len = snprintf (Reply, sizeof (Reply),
                                  "QMOD RAW R%s %s %lu\r\n", Recips[j].To,
                                  Recips[j].ORecip ? Recips[j].
                                  ORecip : Recips[j].To, Recips[j].Flags);
                    NMAPSendCommand (Client->client.conn, Reply, len);
                }
                break;

            case DELIVER_HOST_UNKNOWN:
            case DELIVER_USER_UNKNOWN:
            case DELIVER_BOGUS_NAME:
            case DELIVER_INTERNAL_ERROR:
            case DELIVER_FAILURE:{
                    if (Recips[j].Flags & DSN_FAILURE) {
                        if (Result[0] != '\0') {
                            len = snprintf (Reply, sizeof (Reply),
                                          "QRTS %s %s %lu %d %s\r\n",
                                          Recips[j].To,
                                          Recips[j].ORecip ? Recips[j].
                                          ORecip : Recips[j].To,
                                          Recips[j].Flags, Recips[j].Result,
                                          Result);
                        } else {
                            len = snprintf (Reply, sizeof (Reply),
                                          "QRTS %s %s %lu %d\r\n",
                                          Recips[j].To,
                                          Recips[j].ORecip ? Recips[j].
                                          ORecip : Recips[j].To,
                                          Recips[j].Flags, Recips[j].Result);
                        }
                        NMAPSendCommand (Client->client.conn, Reply, len);
                    }
                }
                break;
            }
        }
        i += rc;
    }

    MemFree(Envelope);
    MemFree(Recips);
}

static void
RelayRemoteEntry (ConnectionStruct * client, unsigned long size, int lines)
{
    int i;
    int rc;
    int j = 0;
    int recipCount = 0;
    unsigned long msgFlags = MSG_FLAG_ENCODING_7BIT;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char *buffer;
    unsigned char *bufferPtr;
    unsigned char from[BUFSIZE + 1];
    unsigned char reply[BUFSIZE + 1];
    unsigned char result[BUFSIZE + 1];
    BOOL local = FALSE;
    RecipStruct *recips;

    if (!client) {
        return;
    }

    lines += 3;
    recips = MemMalloc (size + (lines * sizeof (RecipStruct)));
    if (!recips) {
        Log(LOG_ERROR, "Out of memory File %s %d Line %d",
            __FILE__, size + (lines * sizeof (RecipStruct)), __LINE__);
        return;
    }

    if (client->From != NULL) {
        MemFree (client->From);
    }

    /* This makes sure the recip structure gets freed if we */
    /* go into EndClientConnection for an error or shutdown */
    client->From = (unsigned char *) recips;

    /* This adds lines * sizeof(RecipStruct) */
    buffer = (unsigned char *) (recips + lines);
    bufferPtr = buffer;

    while ((rc = NMAPReadResponse (client->client.conn, reply, sizeof (reply), FALSE)) != 6021) {
        switch (reply[0]) {
        case QUEUE_FROM:{
                strcpy (from, reply + 1);
                NMAPSendCommand (client->client.conn, result, snprintf (result, sizeof (result), "QMOD RAW %s\r\n", reply));
                continue;
            }

        case QUEUE_FLAGS:{
                msgFlags = atoi (reply + 1);
                NMAPSendCommand (client->client.conn, result, snprintf (result, sizeof (result), "QMOD RAW %s\r\n", reply));
                continue;
            }

        case QUEUE_CALENDAR_LOCAL:{
                /* All calendar messages should be delivered locally. */
                NMAPSendCommand (client->client.conn, reply, snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n", reply));
                continue;
            }

        case QUEUE_RECIP_LOCAL:
        case QUEUE_RECIP_MBOX_LOCAL:{
                if (msgFlags & (MSG_FLAG_SOURCE_EXTERNAL | MSG_FLAG_CALENDAR_OBJECT)) {
                    /* Calendar messages should be delivered locally.
                       Externally received messages (relayed here) should be delivered locally. */
                    NMAPSendCommand (client->client.conn, reply, snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n", reply));
                    continue;
                }

                if (msgFlags & MSG_FLAG_SOURCE_EXTERNAL) {
                    /* Reuse space */
                    bufferPtr = recips[recipCount].To;
                    continue;
                }

                /* Fall through */
                /* All internally generated messages should be relayed. */
            }

        case QUEUE_RECIP_REMOTE:{
                memset(recips[recipCount].ORecip, '\0', MAXEMAILNAMESIZE+1);
                recips[recipCount].Flags = DSN_FAILURE;
                recips[recipCount].Result = 0;

                /* R<recipient>[ <original recipient>[ flags]] */
                /* Look for the first optional ' ' delim between <recipient> and <original recipient> */
                ptr = strchr (reply + 1, ' ');
                if (ptr) {
                    *ptr = '\0';
                }

                *bufferPtr = '\0';

                rc = RewriteAddress (client->client.conn, reply + 1, bufferPtr, size - (bufferPtr - buffer));
                if (rc == MAIL_BOGUS) {
                    rc = DELIVER_BOGUS_NAME;
                    recips[recipCount].Result = rc;
                }

                strncpy(recips[recipCount].To, bufferPtr, MAXEMAILNAMESIZE);

                /* Leave NULL terminator */
                bufferPtr += strlen (bufferPtr) + 1;
                if (ptr) {
                    ptr2 = strchr (ptr + 1, ' ');
                    if (ptr2) {
                        *ptr2 = '\0';
                        recips[recipCount].Flags = atoi (ptr2 + 1);
                    }

                    strncpy(recips[recipCount].ORecip, ptr+1, MAXEMAILNAMESIZE);
                    bufferPtr += strlen (bufferPtr) + 1;
                }

                if (recips[recipCount].Result == 0) {
                    switch (rc) {
                    case MAIL_REMOTE:
                    case MAIL_RELAY:{
                            /* All internally generated messages should be relayed */
                            if (!(msgFlags & MSG_FLAG_SOURCE_EXTERNAL)) {
                                recipCount++;
                            }

                            continue;
                        }

                    default:{
                            if (msgFlags &
                                (MSG_FLAG_SOURCE_EXTERNAL |
                                 MSG_FLAG_CALENDAR_OBJECT)) {
                                /* Calendar messages should be delivered locally.
                                   Externally received messages (relayed here) should be delivered locally. */
                                /* The responses from these commands will be in NMAPs send buffer after our ctrl file */
                                /* we read them after the loop */
                                if (!local) {
                                    /* RemoteHost abused for queue id */
                                    NMAPSendCommand (client->client.conn, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QCOPY %s\r\n",
                                                              client->
                                                              RemoteHost));
                                    NMAPSendCommand (client->client.conn, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QSTOR FLAGS %ld\r\n",
                                                              msgFlags));
                                    NMAPSendCommand (client->client.conn, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QSTOR FROM %s\r\n",
                                                              from));
                                    local = TRUE;
                                    j = 0;
                                }

                                if (recips[recipCount].ORecip) {
                                    NMAPSendCommand (client->client.conn, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QSTOR LOCAL %s %s %lu\r\n",
                                                              recips
                                                              [recipCount].To,
                                                              recips
                                                              [recipCount].
                                                              ORecip,
                                                              recips
                                                              [recipCount].
                                                              Flags));
                                }
                                else {
                                    NMAPSendCommand (client->client.conn, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QSTOR LOCAL %s %s %lu\r\n",
                                                              recips
                                                              [recipCount].To,
                                                              recips
                                                              [recipCount].To,
                                                              recips
                                                              [recipCount].
                                                              Flags));
                                }

                                /* Reuse space */
                                bufferPtr = recips[recipCount].To;
                                j++;
                            }
                            else if (!(msgFlags & MSG_FLAG_SOURCE_EXTERNAL)) {
                                /* All internally generated messages should be relayed. */
                                strncpy(recips[recipCount].To, recips[recipCount].ORecip, MAXEMAILNAMESIZE);
                                recipCount++;
                            }

                            continue;
                        }
                    }
                }
                else {
                    recipCount++;
                }

                break;
            }

        default:{
                NMAPSendCommand (client->client.conn, result,
                                snprintf (result, sizeof (result),
                                          "QMOD RAW %s\r\n", reply));
                break;
            }
        }
    }

    if (local) {
        NMAPSendCommand (client->client.conn, "QRUN\r\n", 6);

        /* QCOPY */
        NMAPReadResponse (client->client.conn, reply, sizeof (reply), TRUE);

        /* QSTOR FLAGS */
        NMAPReadResponse (client->client.conn, reply, sizeof (reply), TRUE);

        /* QSTOR FROM */
        NMAPReadResponse (client->client.conn, reply, sizeof (reply), TRUE);

        /* QSTOR LOCALS */
        for (i = 0; i < j; i++) {
            NMAPReadResponse (client->client.conn, reply, sizeof (reply), TRUE);
        }

        /* QRUN */
        NMAPReadResponse (client->client.conn, reply, sizeof (reply), TRUE);
    }

    qsort (recips, recipCount, sizeof (RecipStruct), (void *) RecipCompare);

    i = 0;
    while (i < recipCount) {
        rc = 1;
        while (i + rc < recipCount
               && RecipCompare (&recips[i], &recips[i + rc]) == 0) {
            rc++;
        }
        result[0] = '\0';

        DeliverRemoteMessage (client, from, &recips[i], rc, msgFlags, result,
                              BUFSIZE);

        for (j = i; j < (i + rc); j++) {
            switch (recips[j].Result) {
            case DELIVER_SUCCESS:{
                    if (recips[j].Flags & DSN_SUCCESS) {
                        NMAPSendCommand (client->client.conn, reply,
                                        snprintf (reply, sizeof (reply),
                                                  "QRTS %s %s %lu %d\r\n",
                                                  recips[j].To,
                                                  recips[j].ORecip,
                                                  recips[j].Flags,
                                                  DELIVER_SUCCESS));
                    }
                    break;
                }

            case DELIVER_TIMEOUT:
            case DELIVER_REFUSED:
            case DELIVER_UNREACHABLE:
            case DELIVER_TRY_LATER:{
                    NMAPSendCommand (client->client.conn, reply,
                                    snprintf (reply, sizeof (reply),
                                              "QMOD RAW R%s %s %lu\r\n",
                                              recips[j].To,
                                              recips[j].ORecip ? recips[j].
                                              ORecip : recips[j].To,
                                              recips[j].Flags));
                    break;
                }

            case DELIVER_HOST_UNKNOWN:
            case DELIVER_USER_UNKNOWN:
            case DELIVER_BOGUS_NAME:
            case DELIVER_INTERNAL_ERROR:
            case DELIVER_FAILURE:{
                    if (recips[j].Flags & DSN_FAILURE) {
                        if (result[0] != '\0') {
                            NMAPSendCommand (client->client.conn, reply,
                                            snprintf (reply, sizeof (reply),
                                                      "QRTS %s %s %lu %d %s\r\n",
                                                      recips[j].To,
                                                      recips[j].
                                                      ORecip ? recips[j].
                                                      ORecip : recips[j].To,
                                                      recips[j].Flags,
                                                      recips[j].Result,
                                                      result));
                        }
                        else {
                            NMAPSendCommand (client->client.conn, reply,
                                            snprintf (reply, sizeof (reply),
                                                      "QRTS %s %s %lu %d\r\n",
                                                      recips[j].To,
                                                      recips[j].
                                                      ORecip ? recips[j].
                                                      ORecip : recips[j].To,
                                                      recips[j].Flags,
                                                      recips[j].Result));
                        }
                    }
                    break;
                }
            }
        }

        i += rc;
    }

    /* This includes buffer */
    MemFree (recips);
    client->From = NULL;

    return;
}

static int PullLine2 (unsigned char *Line, unsigned long LineSize, unsigned char **NextLine) {
    unsigned char *ptr;

    ptr = *NextLine;
    *NextLine = strchr (ptr, '\r');
    if (*NextLine) {
        **NextLine = '\0';
        (*NextLine) += 2;
    }

    if (strlen (ptr) < LineSize) {
        strcpy (Line, ptr);
    } else {
        strncpy (Line, ptr, LineSize - 1);
        Line[LineSize - 1] = '\0';
    }

    if (*NextLine) {
        return (0);
    } else {
        return (6021);
    }
}

static int PullLine (unsigned char *Line, unsigned long LineSize, unsigned char **NextLine) {
    unsigned char *ptr;

    ptr = *NextLine;
    *NextLine = strchr (ptr, '\n');
    if (*NextLine) {
        **NextLine = '\0';
        *NextLine = (*NextLine) + 1;
    }

    if (strlen (ptr) < LineSize) {
        strcpy (Line, ptr);
    } else {
        strncpy (Line, ptr, LineSize - 1);
        Line[LineSize - 1] = '\0';
    }

    if (*NextLine) {
        return (0);
    } else {
        return (6021);
    }
}

static void
RelayLocalEntry (ConnectionStruct * client, unsigned long size,
                 unsigned long lines)
{
    int rc;
    int flags;
    unsigned long msgFlags = MSG_FLAG_ENCODING_7BIT;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char *next;
    unsigned char *envelope;
    unsigned char buffer[1024];
    unsigned char to[MAXEMAILNAMESIZE + 1];
    unsigned char from[BUFSIZE + 1];
    unsigned char reply[BUFSIZE + 1];
    BOOL Local = FALSE;

    envelope = MemMalloc (size + 1);
    next = envelope;
    while ((rc = NMAPReadResponse (client->client.conn, buffer, sizeof (buffer) - 1, FALSE)) != 6021) {
        rc = strlen (buffer);
        memcpy (next, buffer, rc);
        next[rc++] = '\n';
        next += rc;
    }

    *next = '\0';
    next = envelope;
    while ((rc = PullLine (buffer, sizeof (buffer) - 1, &next)) != 6021) {
        flags = DSN_FAILURE;
        switch (buffer[0]) {
        case QUEUE_FROM:{
                strcpy (from, buffer + 1);
                NMAPSendCommand (client->client.conn, reply,
                                snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n",
                                          buffer));
                continue;
            }

        case QUEUE_FLAGS:{
                msgFlags = atol (buffer + 1);
                NMAPSendCommand (client->client.conn, reply,
                                snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n",
                                          buffer));
                continue;
            }

        case QUEUE_RECIP_REMOTE:{
                ptr = strchr (buffer + 1, ' ');
                if (ptr) {
                    *ptr = '\0';
                    ptr2 = strchr (ptr + 1, ' ');
                    if (ptr2) {
                        flags = atol (ptr2 + 1);
                    }
                }

                rc = RewriteAddress (client->client.conn, buffer + 1, to, sizeof (to));
                switch (rc) {
                case MAIL_BOGUS:{
                        if (flags & DSN_FAILURE) {
                            NMAPSendCommand (client->client.conn, reply,
                                            snprintf (reply, BUFSIZE,
                                                      "QRTS %s %s %lu %d Addressformat not valid\r\n",
                                                      to, ptr + 1,
                                                      (long unsigned int)
                                                      flags,
                                                      DELIVER_BOGUS_NAME));
                        }

                        continue;
                    }

                case MAIL_LOCAL:{
                        if (msgFlags &
                            (MSG_FLAG_SOURCE_EXTERNAL |
                             MSG_FLAG_CALENDAR_OBJECT)) {
                            /* Calendar messages should be delivered locally.
                               Externally received messages (relayed here) should be delivered locally. */
                            if (!Local) {
                                /* RemoteHost abused for queue id */
                                NMAPSendCommand (client->client.conn, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QCOPY %s\r\n",
                                                          client->
                                                          RemoteHost));
                                NMAPReadResponse (client->client.conn, reply, BUFSIZE, TRUE);  /* QCOPY */

                                NMAPSendCommand (client->client.conn, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR FLAGS %lu\r\n",
                                                          msgFlags));
                                NMAPReadResponse (client->client.conn, reply, BUFSIZE, TRUE);  /* QSTOR FLAGS */

                                NMAPSendCommand (client->client.conn, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR FROM %s\r\n",
                                                          from));
                                NMAPReadResponse (client->client.conn, reply, BUFSIZE, TRUE);  /* QSTOR FROM */

                                Local = TRUE;
                            }

                            if (ptr) {
                                NMAPSendCommand (client->client.conn, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR LOCAL %s %s\r\n",
                                                          to, ptr + 1));
                            }
                            else {
                                NMAPSendCommand (client->client.conn, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR LOCAL %s\r\n",
                                                          to));
                            }

                            NMAPReadResponse (client->client.conn, reply, BUFSIZE, TRUE);      /* QSTOR LOCALS */
                        }
                        else if (!(msgFlags & MSG_FLAG_SOURCE_EXTERNAL)) {
                            if (ptr) {
                                *ptr = ' ';
                            }

                            /* All internally generated messages should be relayed. */
                            NMAPSendCommand (client->client.conn, reply,
                                            snprintf (reply, BUFSIZE,
                                                      "QMOD RAW "
                                                      QUEUES_RECIP_REMOTE
                                                      "%s\r\n", buffer + 1));
                        }

                        continue;
                    }

                case MAIL_REMOTE:
                case MAIL_RELAY:{
                        /* All internally generated messages should be relayed. */
                        if (!(msgFlags & MSG_FLAG_SOURCE_EXTERNAL)) {
                            if (ptr) {
                                *ptr = ' ';
                            }

                            NMAPSendCommand (client->client.conn, reply,
                                            snprintf (reply, BUFSIZE,
                                                      "QMOD RAW "
                                                      QUEUES_RECIP_REMOTE
                                                      "%s\r\n", buffer + 1));
                        }

                        continue;
                    }
                }

                continue;
            }

        case QUEUE_CALENDAR_LOCAL:{
                msgFlags |= MSG_FLAG_CALENDAR_OBJECT;

                /* Fall through */
            }

        case QUEUE_RECIP_LOCAL:
        case QUEUE_RECIP_MBOX_LOCAL:{
                if (msgFlags &
                    (MSG_FLAG_SOURCE_EXTERNAL | MSG_FLAG_CALENDAR_OBJECT)) {
                    /* Calendar messages should be delivered locally.
                       Externally received messages (relayed here) should be delivered locally. */
                    NMAPSendCommand (client->client.conn, reply,
                                    snprintf (reply, BUFSIZE,
                                              "QMOD RAW %s\r\n", buffer));
                }
                else if (!(msgFlags & MSG_FLAG_SOURCE_EXTERNAL)) {
                    /* All internally generated messages should be relayed. */
                    NMAPSendCommand (client->client.conn, reply,
                                    snprintf (reply, BUFSIZE,
                                              "QMOD RAW " QUEUES_RECIP_REMOTE
                                              "%s\r\n", buffer + 1));
                }

                continue;
            }

        default:{
                NMAPSendCommand (client->client.conn, reply,
                                snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n",
                                          buffer));
                continue;
            }
        }
    }

    MemFree (envelope);

    if (Local) {
        NMAPSendCommand (client->client.conn, "QRUN\r\n", 6);
        NMAPReadResponse (client->client.conn, reply, BUFSIZE, TRUE);  /* QRUN */
    }
}

static void
ProcessLocalEntry (ConnectionStruct * Client, unsigned long Size,
                   unsigned long Lines)
{
    unsigned long msgFlags = 0;
    unsigned char Line[1024];
    unsigned char Reply[BUFSIZE + 1];
    unsigned char From[BUFSIZE + 1];
    unsigned char To[MAXEMAILNAMESIZE + 1];
    unsigned char *ptr, *ptr2;
    int len;
    int rc, j;
    int Flags;
    BOOL Local = FALSE;
    unsigned char *Envelope = MemMalloc (Size + 1);
    unsigned char *NextLine = Envelope;

    while ((rc = NMAPReadResponse (Client->client.conn, Line, sizeof (Line), FALSE)) != 6021) {
        rc = strlen (Line);
        memcpy (NextLine, Line, rc);
        NextLine[rc++] = '\n';
        NextLine += rc;
    }
    NextLine[0] = '\0';
    NextLine = Envelope;

    while ((rc = PullLine (Line, sizeof (Line), &NextLine)) != 6021) {
        Flags = DSN_FAILURE;
        switch (Line[0]) {
        case QUEUE_FROM:{
                len =
                    snprintf (Reply, sizeof (Reply), "QMOD RAW %s\r\n", Line);
                NMAPSendCommand (Client->client.conn, Reply, len);
                strcpy (From, Line + 1);
            }
            break;

        case QUEUE_FLAGS:{
                msgFlags = atol (Line + 1);

                NMAPSendCommand (Client->client.conn, Reply,
                                snprintf (Reply, sizeof (Reply),
                                          "QMOD RAW %s\r\n", Line));
                break;
            }

        case QUEUE_RECIP_REMOTE:{
                ptr = strchr (Line + 1, ' ');
                if (ptr) {
                    *ptr = '\0';
                    ptr2 = strchr (ptr + 1, ' ');
                    if (ptr2) {
                        Flags = atol (ptr2 + 1);
                    }
                }

                rc = RewriteAddress (Client->client.conn, Line + 1, To, sizeof (To));
                switch (rc) {
                case MAIL_BOGUS:{
                        if (Flags & DSN_FAILURE) {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QRTS %s %s %lu %d Addressformat not valid\r\n",
                                          To, ptr + 1,
                                          (long unsigned int) Flags,
                                          DELIVER_BOGUS_NAME);
                            NMAPSendCommand (Client->client.conn, Reply, len);
                        }
                        continue;
                        break;
                    }

                case MAIL_RELAY:
                case MAIL_REMOTE:{
                        if (ptr) {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QMOD RAW " QUEUES_RECIP_REMOTE
                                          "%s %s\r\n", To, ptr + 1);
                        }
                        else {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QMOD RAW " QUEUES_RECIP_REMOTE
                                          "%s\r\n", To);
                        }
                        NMAPSendCommand (Client->client.conn, Reply, len);
                        break;
                    }

                default:{
                        if (!Local) {
                            len = snprintf (Reply, sizeof (Reply), "QCOPY %s\r\n", Client->RemoteHost); /* RemoteHost abused for queue id */
                            NMAPSendCommand (Client->client.conn, Reply, len);
                            NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);       /* QCOPY */

                            NMAPSendCommand (Client->client.conn, Reply,
                                            snprintf (Reply, sizeof (Reply),
                                                      "QSTOR FLAGS %ld\r\n",
                                                      msgFlags));
                            NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);       /* QSTOR FLAGS */

                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QSTOR FROM %s\r\n", From);
                            NMAPSendCommand (Client->client.conn, Reply, len);
                            NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);       /* QSTOR FROM */
                            Local = TRUE;
                            j = 0;
                        }
                        if (ptr) {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QSTOR LOCAL %s %s\r\n", To,
                                          ptr + 1);
                        }
                        else {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QSTOR LOCAL %s\r\n", To);
                        }
                        NMAPSendCommand (Client->client.conn, Reply, len);
                        NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);   /* QSTOR LOCALS */
                        j++;
                        break;
                    }
                }
                break;
            }

        default:{
                len =
                    snprintf (Reply, sizeof (Reply), "QMOD RAW %s\r\n", Line);
                NMAPSendCommand (Client->client.conn, Reply, len);
                break;
            }
        }
    }

    MemFree (Envelope);

    if (Local) {
        NMAPSendCommand (Client->client.conn, "QRUN\r\n", 6);
        NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);   /* QRUN */
    }
}

static void
HandleQueueConnection (void *ClientIn)
{
    int ReplyInt, Queue = Q_INCOMING;
    unsigned char Reply[1024];
    ConnectionStruct *Client = (ConnectionStruct *) ClientIn;
    unsigned char *ptr;
    unsigned long Lines;
    unsigned long Size;

    Client->State = STATE_WAITING;

    ReplyInt = NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);

    if (ReplyInt != 6020) {
        NMAPSendCommand (Client->client.conn, "QDONE\r\n", 7);
        EndClientConnection (Client);
        return;
    }
    if (Reply[3] == '-') {
        Reply[3] = '\0';
        Queue = atoi (Reply);
        Reply[3] = '-';
    }
    ptr = strchr (Reply, ' ');
    if (!ptr) {
        EndClientConnection (Client);
        return;
    }
    *ptr = '\0';

    if (strlen (Reply) < sizeof (Client->RemoteHost)) {
        strcpy (Client->RemoteHost, Reply);     /* Abuse RemoteHost for the queue ID */
    }
    else {
        EndClientConnection (Client);
        return;
    }

    Size = atol (ptr + 1);

    ptr = strchr (ptr + 1, ' ');
    if (!ptr) {
        EndClientConnection (Client);
        return;
    }
    Client->Flags = atol (ptr + 1);

    ptr = strchr (ptr + 1, ' ');
    if (!ptr) {
        EndClientConnection (Client);
        return;
    }
    Lines = atol (ptr + 1);

    if (!SMTP.relay_local) {
        if (Queue == Q_OUTGOING) {
            ProcessRemoteEntry (Client, Size, Lines);
        }
        else {
            ProcessLocalEntry (Client, Size, Lines);
        }
    }
    else {
        if (Queue == Q_OUTGOING) {
            RelayRemoteEntry (Client, Size, Lines);
        }
        else {
            RelayLocalEntry (Client, Size, Lines);
        }
    }

    NMAPSendCommand (Client->client.conn, "QDONE\r\n", 7);
    NMAPReadResponse (Client->client.conn, Reply, sizeof (Reply), TRUE);

    EndClientConnection (Client);
    return;
}

static void
QueueServerStartup (void *ignored)
{
    Connection *conn;
    ConnectionStruct *client;
    int ccode;
    XplThreadID id = 0;
    BOOL qd = FALSE, qo = FALSE;

    XplRenameThread (XplGetThreadID (), "SMTP NMAP Q Monitor");

    SMTPQServerConnection = ConnAlloc(FALSE);
    if (!SMTPQServerConnection) {
        raise(SIGTERM);
        return;
    }

    memset(&(SMTPQServerConnection->socketAddress), 0, sizeof(SMTPQServerConnection->socketAddress));

    SMTPQServerConnection->socketAddress.sin_family = AF_INET;
    SMTPQServerConnection->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
    SMTPQServerConnection->socket = ConnServerSocket(SMTPQServerConnection, 2048);
    if (SMTPQServerConnection->socket == -1) {
        ConnFree(SMTPQServerConnection);
        raise(SIGTERM);
        return;
    }

    /* register on the two queues we need to be on */
    while (!qd && !qo) {
        if (!qd) {
            qd = (QueueRegister(MSGSRV_AGENT_SMTP, Q_DELIVER, SMTPQServerConnection->socketAddress.sin_port) == REGISTRATION_COMPLETED);
        }
        if (!qo) {
            qo = (QueueRegister(MSGSRV_AGENT_SMTP, Q_OUTGOING, SMTPQServerConnection->socketAddress.sin_port) == REGISTRATION_COMPLETED);
        }
        if (Exiting) {
            ConnFree(SMTPQServerConnection);
            raise(SIGTERM);
            return;
        }
        if (!qd || !qo) {
            Log(LOG_ERROR, "Could not register with bongoqueue, sleeping for 3 seconds");
            XplDelay(3000);
        }
    };

    while (!Exiting) {
        if (ConnAccept(SMTPQServerConnection, &conn) != -1) {
            if (!Exiting) {
                client = GetSMTPConnection();
                if (client) {
                    /* this may seem wrong here, but really the nmap agent is the
                     * client here as it connected to us */
                    client->client.conn = conn;
                    XplBeginCountedThread (&id, HandleQueueConnection, STACKSIZE_Q, client, ccode, SMTPQueueThreads);
                    if (ccode == 0) {
                        continue;
                    }
                    ReturnSMTPConnection(client);
                }
                /* GetSMTPConnection failed */
                ConnClose(conn, 1);
                ConnFree(conn);
                continue;
            }
            ConnClose(conn, 1);
            ConnFree(conn);
        }
    }

    XplSafeDecrement (SMTPServerThreads);

    Log(LOG_DEBUG, "Queue monitor thread done.");

    return;
}

static void
SMTPShutdownSigHandler (int sigtype)
{
    static BOOL signaled = FALSE;

    if (!signaled && (sigtype == SIGTERM || sigtype == SIGINT)) {
        signaled = Exiting = TRUE;

        if (SMTPServerConnection) {
            ConnClose(SMTPServerConnection, 1);
            ConnFree(SMTPServerConnection);
            SMTPServerConnection = NULL;
        }

        XplSignalLocalSemaphore (SMTPShutdownSemaphore);
    } else if (signaled && (sigtype == SIGTERM || sigtype == SIGINT)) {
        XplExit(0);
    }

    return;
}

static int
ServerSocketInit (void)
{
    int ccode;

    SMTPServerConnection = ConnAlloc(FALSE);
    if (!SMTPServerConnection) {
        Log(LOG_ERROR, "Could not allocate the connection.");
        return -1;
    }

    SMTPServerConnection->socketAddress.sin_family = AF_INET;
    SMTPServerConnection->socketAddress.sin_port = htons(SMTP.port);
    SMTPServerConnection->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

    /* Get root privs back for the bind.  It's ok if this fails - 
    * the user might not need to be root to bind to the port */
    XplSetEffectiveUserId (0);

    SMTPServerConnection->socket = ConnServerSocket(SMTPServerConnection, 2048);

    /* drop the privs back */
    if (XplSetEffectiveUser (MsgGetUnprivilegedUser ()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user '%s'", MsgGetUnprivilegedUser ());
        return -1;
    }

    if (SMTPServerConnection->socket < 0) {
        ccode = SMTPServerConnection->socket;
        Log(LOG_ERROR, "Could not allocate SMTP socket");
        ConnFree(SMTPServerConnection);
        return ccode;
    }
    return 0;
}

static int
ServerSocketSSLInit (void)
{
    int ccode;

    SMTPServerConnectionSSL = ConnAlloc(FALSE);
    if (SMTPServerConnectionSSL == NULL) {
        Log(LOG_ERROR, "Could not allocate the SSL connection");
        ConnFree(SMTPServerConnection);
        return -1;
    }

    SMTPServerConnectionSSL->socketAddress.sin_family = AF_INET;
    SMTPServerConnectionSSL->socketAddress.sin_port = htons (SMTP.port_ssl);
    SMTPServerConnectionSSL->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress ();

    /* Get root privs back for the bind.  It's ok if this fails - 
     * the user might not need to be root to bind to the port */
    XplSetEffectiveUserId (0);

    SMTPServerConnectionSSL->socket = ConnServerSocket(SMTPServerConnectionSSL, 2048);
    /* drop the privs back */
    if (XplSetEffectiveUser (MsgGetUnprivilegedUser ()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user '%s' for SSL", MsgGetUnprivilegedUser ());
        return -1;
    }

    if (SMTPServerConnectionSSL->socket < 0) {
        ccode = SMTPServerConnectionSSL->socket;
        Log(LOG_ERROR, "Could not allocate SSL socket");
        ConnFree(SMTPServerConnection);
        return ccode;
    }
    return 0;
}

static void
SMTPServer (void *ignored)
{
    Connection *conn;
    int ccode;
    int arg;
    int oldTGID;
    ConnectionStruct *client;
    XplThreadID id;

    XplSafeIncrement (SMTPServerThreads);
    XplRenameThread (XplGetThreadID(), "SMTP Server");

    while (!Exiting) {
        if (ConnAccept(SMTPServerConnection, &conn) != -1) {
            if (!Exiting) {
                if (!SMTPReceiverStopped) {
                    if (XplSafeRead(SMTPConnThreads) < SMTP.max_thread_load) {
                        client = GetSMTPConnection();
                        if (client) {
                            client->client.conn = conn;
                            XplBeginCountedThread(&id, HandleConnection, STACKSIZE_S, client, ccode, SMTPConnThreads);
                            if (ccode == 0) {
                                continue;
                            }
                            ReturnSMTPConnection(client);
                        }
                        /* GetSMTPConnection failed */

                        ConnSend(conn, MSG500NOMEMORY, MSG500NOMEMORY_LEN);
                        ConnClose(conn, 1);
                        ConnFree(conn);
                        continue;
                    } else {
                        /* No more threads available */
                        ConnSend(conn, MSG451NOCONNECTIONS, MSG451NOCONNECTIONS_LEN);
                        ConnClose(conn, 1);
                        ConnFree(conn);
                    }
                } else {
                    /* RecieverStopped */
                    ConnSend(conn, MSG451RECEIVERDOWN, MSG451RECEIVERDOWN_LEN);
                    ConnClose(conn, 1);
                    ConnFree(conn);
                }
            }
            ConnClose(conn, 1);
            ConnFree(conn);
            continue;
        }

        if (!Exiting) {
            switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO:
#endif
            case EINTR:{
                    Log(LOG_ERROR, "Accept failure %s Errno %d", "Server", errno);
                    continue;
                }

            case EIO:{
                    Log(LOG_ERROR, "Accept failure Errno %d %d", errno, 2);
                    Exiting = TRUE;

                    break;
                }

            default:{
                    Log(LOG_ALERT, "Accept failure: Errno %d", errno);

                    break;
                }
            }

            break;
        }

        /*      Shutdown signaled!      */
        break;
    }

/*	Shutting down!	*/
    Exiting = TRUE;

    oldTGID = XplSetThreadGroupID (TGid);

    Log(LOG_DEBUG, "Closing server socket");

    if (SMTPServerConnection) {
        ConnClose(SMTPServerConnection, 1);
        ConnFree(SMTPServerConnection);
        SMTPServerConnection = NULL;
    }
    if (SMTPServerConnectionSSL) {
        ConnClose(SMTPServerConnectionSSL, 1);
        ConnFree(SMTPServerConnectionSSL);
        SMTPServerConnectionSSL = NULL;
    }

    if (SMTPQServerConnection) {
        ConnClose(SMTPQServerConnection, 1);
        ConnFree(SMTPQServerConnection);
        SMTPQServerConnection = NULL;
    }

    if (SMTPQServerConnectionSSL) {
        ConnClose(SMTPQServerConnectionSSL, 1);
        ConnFree(SMTPQServerConnectionSSL);
        SMTPQServerConnectionSSL = NULL;
    }

    /*      Wake up the children and set them free! */
    /* fixme - SocketShutdown; */

    /*      Wait for the our siblings to leave quietly.     */
    for (arg = 0; (XplSafeRead (SMTPServerThreads) > 1) && (arg < 60); arg++) {
        XplDelay (1000);
    }

    if (XplSafeRead (SMTPServerThreads) > 1) {
        Log(LOG_ERROR, "%d server threads outstanding; attempting forceful unload.", XplSafeRead(SMTPServerThreads)-1);
    }

    Log(LOG_DEBUG, "Shutting down %d conn client threads and %d queue client threads.", XplSafeRead(SMTPConnThreads), XplSafeRead(SMTPQueueThreads));

    /*      Make sure the kids have flown the coop. */
    for (arg = 0; (XplSafeRead (SMTPConnThreads) + XplSafeRead (SMTPQueueThreads)) && (arg < 3 * 60); arg++) {
        XplDelay (1000);
    }

    if (XplSafeRead (SMTPConnThreads) + XplSafeRead (SMTPQueueThreads)) {
        Log(LOG_ERROR, "%d threads outstanding; attempting forceful unload.", XplSafeRead(SMTPConnThreads)+XplSafeRead(SMTPQueueThreads));
    }

    Log(LOG_DEBUG, "Removing SSL data");

    /* Cleanup SSL */
    if (SSLContext) {
        ConnSSLContextFree(SSLContext);
        SSLContext = NULL;
    }

    LogShutdown ();

    XplRWLockDestroy (&ConfigLock);
    MsgShutdown ();

    ConnShutdown ();

    MemPrivatePoolFree (SMTPConnectionPool);
    MemoryManagerClose (MSGSRV_AGENT_SMTP);

    Log(LOG_DEBUG, "Shutdown complete.");

    XplSignalLocalSemaphore (SMTPServerSemaphore);
    XplWaitOnLocalSemaphore (SMTPShutdownSemaphore);

    XplCloseLocalSemaphore (SMTPShutdownSemaphore);
    XplCloseLocalSemaphore (SMTPServerSemaphore);

    XplSetThreadGroupID (oldTGID);

    return;
}

static void
SMTPSSLServer (void *ignored)
{
    Connection *conn;
    int ccode;
    int arg;
    unsigned char *message;
    ConnectionStruct *client;
    XplThreadID id = 0;

    XplRenameThread (XplGetThreadID (), "SMTP SSL Server");

    while(!Exiting) {
        if (ConnAccept(SMTPServerConnectionSSL, &conn) != -1) {
            conn->ssl.enable = TRUE;
            if (!Exiting) {
                if (!SMTPReceiverStopped) {
                    if (XplSafeRead(SMTPConnThreads) < SMTP.max_thread_load) {
                        client = GetSMTPConnection();
                        if (client) {
                            client->client.conn = conn;
                            XplBeginCountedThread (&id, HandleConnection, STACKSIZE_S, client, ccode, SMTPConnThreads);
                            if (ccode == 0) {
                                continue;
                            }
                            ReturnSMTPConnection(client);
                            ConnClose(conn, 1);
                            ConnFree(conn);
                            continue;
                        } else {
                            /* GetSMTPConnection() failed */
                            message = MSG500NOMEMORY;
                            arg = MSG500NOMEMORY_LEN;
                        }
                    } else {
                        /* no more threads */
                        message = MSG451NOCONNECTIONS;
                        arg = MSG451NOCONNECTIONS_LEN;
                    }
                } else {
                    /* receiver stopped */
                    message = MSG451RECEIVERDOWN;
                    arg = MSG451RECEIVERDOWN_LEN;
                }

                if (ConnNegotiate(conn, SSLContext)) {
                    if (ConnWrite(conn, message, arg) != -1) {
                        ConnFlush(conn);
                    }
                    ConnClose(conn, 1);
                    ConnFree(conn);
                    continue;
                }

                ConnSend(conn, message, arg);
                ConnClose(conn, 1);
                ConnFree(conn);
                continue;
            }

            break;
        }

        if (!Exiting) {
            switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO:
#endif
            case EINTR:{
                    Log(LOG_ERROR, "Accept failure %s Errno %d", "SSL Server", errno);
                    continue;
                }
            case EIO:{
                    Log(LOG_ERROR, "Accept failure Errno %d %d", errno, 2);
                    Exiting = TRUE;
                    break;
                }
            default:{
                    Log(LOG_ALERT, "Accept failure Errno %d", errno);
                    break;
                }
            }
            break;
        }
    }

    XplSafeDecrement (SMTPServerThreads);

    Log(LOG_DEBUG, "SSL Listening thread terminated.");

    if (SMTPServerConnectionSSL) {
        ConnFree(SMTPServerConnectionSSL);
        SMTPServerConnectionSSL = NULL;
    }

    if (!Exiting) {
        raise (SIGTERM);
    }

    return;
}

#define	SetPtrToValue(Ptr,String)	Ptr=String;while(isspace(*Ptr)) Ptr++;if ((*Ptr=='=') || (*Ptr==':')) Ptr++; while(isspace(*Ptr)) Ptr++;

static BOOL
ReadConfiguration (void)
{
/*
    struct sockaddr_in soc_address;
    struct sockaddr_in *sin = &soc_address;

    soc_address.sin_addr.s_addr = XplGetHostIPAddress ();
    sprintf (SMTP.hostaddr, "[%d.%d.%d.%d]", sin->sin_addr.s_net,
             sin->sin_addr.s_host, sin->sin_addr.s_lh, sin->sin_addr.s_impno);
*/
    // FIXME: Does SMTP need to know about the various domains? Or will queue fix that?

    /* FIXME. This should be replaced by pulling config from the store. */
#if 0
    if (MsgReadIP (MSGSRV_AGENT_SMTP, MSGSRV_A_QUEUE_SERVER, Config)) {
        strcpy (NMAPServer, Config->Value[0]);
        Log(LOG_INFO, "Configuration string %s Value %s",
            "MSGSRV_A_QUEUE_SERVER", Config->Value[0]);
    }
    MDBFreeValues (Config);
#endif

    LocalAddress = MsgGetHostIPAddress ();

    MsgGetServerCredential(&NMAPHash);

    return (TRUE);
}

XplServiceCode (SMTPShutdownSigHandler)

int XplServiceMain (int argc, char *argv[])
{
    int ccode;
    XplThreadID ID;

    LogStart();
    /* Done binding to ports, drop privs permanently */
    if (XplSetEffectiveUser (MsgGetUnprivilegedUser ()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user %s", MsgGetUnprivilegedUser());
        return 1;
    }
    XplInit();

    XplSignalHandler (SMTPShutdownSigHandler);

    XplSafeWrite (SMTPServerThreads, 0);
    XplSafeWrite (SMTPQueueThreads, 0);
    XplSafeWrite (SMTPConnThreads, 0);
    XplSafeWrite (SMTPIdleConnThreads, 0);

    TGid = XplGetThreadGroupID ();

    if (MemoryManagerOpen (MSGSRV_AGENT_SMTP) == TRUE) {
        SMTPConnectionPool =
            MemPrivatePoolAlloc ("SMTP Connections",
                                 sizeof (ConnectionStruct), 0, 3072, TRUE,
                                 FALSE, SMTPConnectionAllocCB, NULL, NULL);
        if (SMTPConnectionPool != NULL) {
            XplOpenLocalSemaphore (SMTPServerSemaphore, 0);
            XplOpenLocalSemaphore (SMTPShutdownSemaphore, 1);
        }
        else {
            MemoryManagerClose (MSGSRV_AGENT_SMTP);
            Log(LOG_ERROR, "Unable to create connection pool, shutting down");
            return (-1);
        }
    }
    else {
        Log(LOG_ERROR, "Unable to initialize memory manager, shutting down");
        return (-1);
    }
    
    MsgInit();
    MsgAuthInit();

    // FIXME: Connio socket timeout needs to be a run-time tunable. bug #9924
    // ConnStartup (SMTP.socket_timeout, TRUE);
    ConnStartup(600, TRUE);

    NMAPInitialize();

    XplRWLockInit (&ConfigLock);

    for (ccode = 1; argc && (ccode < argc); ccode++) {
        if (XplStrNCaseCmp (argv[ccode], "--forwarder=", 12) == 0) {
            SMTP.use_relay = TRUE;
            strcpy (SMTP.relay_host, argv[ccode] + 12);
        }
    }

    if (! ReadBongoConfiguration(SMTPConfig, "smtp")) {
        Log(LOG_ERROR, "Unable to read SMTP configuration from the store.");
        exit(-1);
    }
    if (! ReadBongoConfiguration(GlobalConfig, "global")) {
        Log(LOG_ERROR, "Unable to read Global configration from the store.");
        exit(-1);
    }
    ReadConfiguration ();

    if (ServerSocketInit () < 0) {
        Log(LOG_WARN, "Can't open ports, exiting");
        return 1;
    }

    XplBeginCountedThread (&ID, QueueServerStartup, STACKSIZE_Q, NULL, ccode, SMTPServerThreads);

    if (SMTP.allow_client_ssl) {
        if (!ServerSocketSSLInit()) {
            ConnSSLConfiguration sslconfig;
            sslconfig.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
            sslconfig.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
            sslconfig.key.type = GNUTLS_X509_FMT_PEM;

            SSLContext = ConnSSLContextAlloc(&sslconfig);
            if (SSLContext) {
                XplBeginCountedThread (&ID, SMTPSSLServer, STACKSIZE_S, NULL, ccode, SMTPServerThreads);
            } else {
                SMTP.allow_client_ssl = FALSE;
            }
        } else {
            SMTP.allow_client_ssl = FALSE;
        }
    }

    /* Done binding to ports, drop privs permanently */
    if (XplSetRealUser (MsgGetUnprivilegedUser ()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user %s", MsgGetUnprivilegedUser());
        return 1;
    }

    XplStartMainThread (PRODUCT_SHORT_NAME, &ID, SMTPServer, 8192, NULL, ccode);

    XplUnloadApp (XplGetThreadID ());
    return (0);
}
