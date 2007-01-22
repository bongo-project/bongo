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
 ****************************************************************************/

#include <config.h>

/* Compile-time options */
#define	NETDB_USE_INTERNET	1
#undef	USE_HOPCOUNT_DETECTION

/* Product defines */
#define	PRODUCT_SHORT_NAME	"smtpd.nlm"
#define	PRODUCT_NAME		"Bongo SMTP Agent"
#define	PRODUCT_DESCRIPTION	"Allows mail-clients and other mail-servers to deliver mail to this host via the SMTP protocol. (SMTP = Simple Mail Transfer Protocol, RFC 2821)"
#define	PRODUCT_VERSION		"$Revision: 1.7 $"
#define NMLOGID_H_NEED_LOGGING_KEY
#define NMLOGID_H_NEED_LOGGING_CERT
#define LOGGERNAME "smtpd"
#include <xpl.h>
#include <connio.h>

#include <msgapi.h>
#include <xplresolve.h>

#include <log4c.h>
#include <logger.h>

#include <mdb.h>
#include <nmap.h>
#include <cmlib.h>

/*	Management Client	Header Include	*/
#include <management.h>
#include <bongoutil.h>
#include "smtpd.h"

char nwinet_scratch[18] = { 0 };

/* Globals */
BOOL Exiting = FALSE;
XplSemaphore SMTPShutdownSemaphore;
XplSemaphore SMTPServerSemaphore;
unsigned long SMTPMaxThreadLoad = 100000;
XplAtomic SMTPServerThreads;
XplAtomic SMTPConnThreads;
XplAtomic SMTPIdleConnThreads;
XplAtomic SMTPQueueThreads;
int SMTPServerSocket = -1;
int SMTPServerSocketSSL = -1;
unsigned long SMTPServerPort = SMTP_PORT;
unsigned long SMTPServerPortSSL = SMTP_PORT_SSL;
int SMTPQServerSocket = -1;
int SMTPQServerSocketPort = -1;
int SMTPQServerSocketSSL = -1;
int SMTPQServerSocketSSLPort = -1;
unsigned char Hostname[MAXEMAILNAMESIZE + 128];
unsigned char Hostaddr[MAXEMAILNAMESIZE + 1];
int TGid;
unsigned long MaxMXServers = 0;

unsigned char **Domains = NULL;
int DomainCount = 0;
unsigned char **UserDomains = NULL;
int UserDomainCount = 0;
unsigned char **RelayDomains = NULL;
int RelayDomainCount = 0;

/* UBE measures */
unsigned long UBEConfig = 0;
XplRWLock ConfigLock;
BOOL SMTPReceiverStopped = FALSE;

unsigned char Postmaster[MAXEMAILNAMESIZE + 1] = "admin";
unsigned char OfficialName[MAXEMAILNAMESIZE + 1] = "";
unsigned char RelayHost[MAXEMAILNAMESIZE + 1] = "";
BOOL UseRelayHost = FALSE;
BOOL RelayLocalMail = FALSE;
BOOL Notify = FALSE;
BOOL AllowClientSSL = FALSE;
BOOL UseNMAPSSL = FALSE;
BOOL AllowEXPN = FALSE;
BOOL AllowVRFY = FALSE;
BOOL CheckRCPT = FALSE;
BOOL SendETRN = FALSE;
BOOL AcceptETRN = FALSE;
BOOL DomainUsernames = TRUE;
BOOL BlockRTSSpam = FALSE;
time_t LastBounce;
time_t BounceInterval;
unsigned long MaxBounceCount;
unsigned long BounceCount;
unsigned char NMAPServer[20] = "127.0.0.1";
unsigned long MessageLimit = 0;
unsigned long MaximumRecipients = 15;
unsigned long MaxNullSenderRecips = ULONG_MAX;
unsigned long MaxFloodCount = 1000;
unsigned long ClientSSLOptions = 0;
unsigned long LocalAddress = 0;
SSL_CTX *SSLContext = NULL;
SSL_CTX *SSLClientContext = NULL;
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
unsigned long SocketTimeout = 10 * 60;
/* Seconds */
#define	MAILBOX_TIMEOUT 15

#define ChopNL(String) { unsigned char *pTr; pTr=strchr((String), 0x0a); if (pTr) *pTr='\0'; pTr=strrchr((String), 0x0d); if (pTr) *pTr='\0'; }

typedef struct
{
    int s;                      /* Socket               */
    int NMAPs;                  /* NMAP Socket          */
    unsigned long State;        /* Connection state     */
    unsigned long Flags;        /* Status flags         */
    unsigned long RecipCount;   /* Number of recipients */
    NMAPMessageFlags MsgFlags;  /* current msg flags    */
    struct sockaddr_in cs;      /* Client info          */
    SSL *CSSL;
    SSL *NSSL;
    unsigned char RemoteHost[MAXEMAILNAMESIZE + 1]; /* Name of remote host   */
    unsigned long BufferPtr;                        /* Current input pointer */
    unsigned char Buffer[BUFSIZE + 1];              /* Input buffer          */
    unsigned long SBufferPtr;                       /* Current send pointer  */
    unsigned char SBuffer[MTU + 1];                 /* Send buffer           */
    unsigned long NBufferPtr;                       /* Current send pointer  */
    unsigned char NBuffer[MTU + 1];                 /* Send buffer           */
    unsigned char Command[BUFSIZE + 1];             /* Current command       */
    unsigned char *From;                            /* For Routing Disabled  */
    unsigned char *AuthFrom;                     /* Sender Authenticated As  */
    unsigned char User[64];                      /* Fixme - Make this bigger */
    unsigned char ClientSSL;
    unsigned char NMAPSSL;
} ConnectionStruct;

typedef struct
{
    unsigned char serverAddress[20];
    unsigned short serverPort;
    BOOL SSL;
    int Queue;
    int agentPort;
} QueueStartStruct;

typedef struct
{
    unsigned char *To;
    unsigned char *ORecip;
    unsigned long Flags;
    int Result;
} RecipStruct;

struct _SMTPD_STATISTICS
{
    XplAtomic IncomingQueueAgent;
    XplAtomic WrongPassword;

    struct
    {
        XplAtomic Serviced;
        XplAtomic Incoming;
        XplAtomic ClosedOut;
        XplAtomic Outgoing;
    } Server;

    struct
    {
        XplAtomic AddressBlocked;
        XplAtomic MAPSBlocked;
        XplAtomic NoDNSEntry;
        XplAtomic DeniedRouting;
        XplAtomic Queue;
    } SPAM;

    struct
    {
        struct
        {
            XplAtomic Remote;
            XplAtomic Local;
        } Received;

        struct
        {
            XplAtomic Remote;
        } Stored;
    } Recipient;

    struct
    {
        struct
        {
            XplAtomic Local;
            XplAtomic Remote;
        } Received;

        struct
        {
            XplAtomic Remote;
        } Stored;
    } Byte;

    struct
    {
        struct
        {
            XplAtomic Local;
            XplAtomic Remote;
        } Received;

        struct
        {
            XplAtomic Remote;
        } Stored;
    } Message;
} SMTPStats;

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

MDBHandle SMTPDirectoryHandle = NULL;

int ClientRead (ConnectionStruct * Client,
                unsigned char *Buf, int Len, int Flags);
int ClientReadSSL (ConnectionStruct * Client,
                   unsigned char *Buf, int Len, int Flags);
int ClientWrite (ConnectionStruct * Client,
                 unsigned char *Buf, int Len, int Flags);
int ClientWriteSSL (ConnectionStruct * Client,
                    unsigned char *Buf, int Len, int Flags);
int NMAPRead (ConnectionStruct * Client,
              unsigned char *Buf, int Len, int Flags);
int NMAPReadSSL (ConnectionStruct * Client,
                 unsigned char *Buf, int Len, int Flags);
int NMAPWrite (ConnectionStruct * Client,
               unsigned char *Buf, int Len, int Flags);
int NMAPWriteSSL (ConnectionStruct * Client,
                  unsigned char *Buf, int Len, int Flags);

typedef int (*IOFunc) (ConnectionStruct * Client,
                       unsigned char *Buf, int Len, int Flags);
IOFunc FuncTbl[8] = {
    NMAPWrite, NMAPWriteSSL,
    NMAPRead, NMAPReadSSL,
    ClientWrite, ClientWriteSSL,
    ClientRead, ClientReadSSL
};

#define DoNMAPWrite(Client, Buf, Len, Flags)   FuncTbl[0+Client->NMAPSSL](Client, Buf, Len, Flags)
#define DoNMAPRead(Client, Buf, Len, Flags)    FuncTbl[2+Client->NMAPSSL](Client, Buf, Len, Flags)
#define DoClientWrite(Client, Buf, Len, Flags) FuncTbl[4+Client->ClientSSL](Client, Buf, Len, Flags)
#define DoClientRead(Client, Buf, Len, Flags)  FuncTbl[6+Client->ClientSSL](Client, Buf, Len, Flags)

/* Prototypes */
void ProcessRemoteEntry (ConnectionStruct * Client,
                         unsigned long Size, int Lines);
void FreeDomains (void);
BOOL SendNMAPServer (ConnectionStruct * Client, unsigned char *Data, int Len);
int GetNMAPAnswer (ConnectionStruct * Client, unsigned char *Reply,
                   unsigned long ReplyLen, BOOL CheckForResult);
int RewriteAddress (unsigned char *Source, unsigned char *Target,
                    int TargetSize);
BOOL FlushClient (ConnectionStruct * Client);
void RegisterNMAPServer (void *QSIn);
BOOL EndClientConnection (ConnectionStruct * Client);
void FreeUserDomains (void);
void FreeRelayDomains (void);

/*	Management Client	Declarations	*/
BOOL ReadSMTPVariable (unsigned int Variable, unsigned char *Data,
                       size_t * DataLength);
BOOL WriteSMTPVariable (unsigned int Variable, unsigned char *Data,
                        size_t DataLength);

ManagementVariables SMTPManagementVariables[] = {
    /* 
     * IMPORTANT: Add items at the end of the array and do not delete
     * items without updating the numbering for all items following it,
     * both in the comments below and in the switch statement in
     * ReadSMTPVariable. 
     */
    /*  0  XplAtomic      SMTPStats.IncomingQueueAgent        */
    {SMTPMV_INCOMING_QUEUE_AGENT, SMTPMV_INCOMING_QUEUE_AGENT_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  1  XplAtomic      SMTPStats.Server.Serviced           */
    {SMTPMV_SERVICED_SERVERS, SMTPMV_SERVICED_SERVERS_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  2  XplAtomic      SMTPStats.Server.Incoming           */
    {SMTPMV_INCOMING_SERVERS, SMTPMV_INCOMING_SERVERS_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  3  XplAtomic      SMTPStats.Server.ClosedOut          */
    {SMTPMV_CLOSED_OUT_SERVERS, SMTPMV_CLOSED_OUT_SERVERS_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  4  XplAtomic      SMTPStats.Server.Outgoing           */
    {SMTPMV_OUTGOING_SERVERS, SMTPMV_OUTGOING_SERVERS_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  5  XplAtomic      SMTPStats.SPAM.AddressBlocked       */
    {SMTPMV_SPAM_ADDRS_BLOCKED, SMTPMV_SPAM_ADDRS_BLOCKED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  6  XplAtomic      SMTPStats.SPAM.MAPSBlocked          */
    {SMTPMV_SPAM_MAPS_BLOCKED, SMTPMV_SPAM_MAPS_BLOCKED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  7  XplAtomic      SMTPStats.SPAM.NoDNSEntry           */
    {SMTPMV_SPAM_NO_DNS_ENTRY, SMTPMV_SPAM_NO_DNS_ENTRY_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  8  XplAtomic      SMTPStats.SPAM.DeniedRouting        */
    {SMTPMV_SPAM_DENIED_ROUTING, SMTPMV_SPAM_DENIED_ROUTING_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /*  9  XplAtomic      SMTPStats.SPAM.Queue                */
    {SMTPMV_SPAM_QUEUE, SMTPMV_SPAM_QUEUE_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 10  XplAtomic      SMTPStats.Recipient.Received.Remote */
    {SMTPMV_REMOTE_RECIP_RECEIVED, SMTPMV_REMOTE_RECIP_RECEIVED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 11  XplAtomic      SMTPStats.Recipient.Received.Local  */
    {SMTPMV_LOCAL_RECIP_RECEIVED, SMTPMV_LOCAL_RECIP_RECEIVED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 12  XplAtomic      SMTPStats.Recipient.Stored.Remote   */
    {SMTPMV_REMOTE_RECIP_STORED, SMTPMV_REMOTE_RECIP_STORED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 13  XplAtomic      SMTPStats.Byte.Received.Remote      */
    {SMTPMV_REMOTE_BYTES_RECEIVED, SMTPMV_REMOTE_BYTES_RECEIVED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 14  XplAtomic      SMTPStats.Byte.Received.Local       */
    {SMTPMV_LOCAL_BYTES_RECEIVED, SMTPMV_LOCAL_BYTES_RECEIVED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 15  XplAtomic      SMTPStats.Byte.Stored.Remote        */
    {SMTPMV_REMOTE_BYTES_STORED, SMTPMV_REMOTE_BYTES_STORED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 16  XplAtomic      SMTPStats.Message.Received.Remote   */
    {SMTPMV_REMOTE_MSGS_RECEIVED, SMTPMV_REMOTE_MSGS_RECEIVED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 17  XplAtomic      SMTPStats.Message.Received.Local    */
    {SMTPMV_LOCAL_MSGS_RECEIVED, SMTPMV_LOCAL_MSGS_RECEIVED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 18  XplAtomic      SMTPStats.Message.Stored.Remote     */
    {SMTPMV_REMOTE_MSGS_STORED, SMTPMV_REMOTE_MSGS_STORED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 19  XplAtomic      SMTPServerThreads;                  */
    {DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP,
     ReadSMTPVariable, NULL},
    /* 20  XplAtomic      SMTPConnThreads;                    */
    {DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP,
     ReadSMTPVariable, NULL},
    /* 21  XplAtomic      SMTPIdleConnThreads;                */
    {DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP,
     ReadSMTPVariable, NULL},
    /* 22  XplAtomic      SMTPQueueThreads;                   */
    {DMCMV_QUEUE_THREAD_COUNT, DMCMV_QUEUE_THREAD_COUNT_HELP,
     ReadSMTPVariable, NULL},
    /* 23  unsigned long  SMTPMaxThreadLoad                   */
    {DMCMV_MAX_CONNECTION_COUNT, DMCMV_MAX_CONNECTION_COUNT_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 24  unsigned long  MaxMXServers                        */
    {SMTPMV_MAX_MX_SERVERS, SMTPMV_MAX_MX_SERVERS_HELP, ReadSMTPVariable,
     NULL},
    /* 25  unsigned long  MaxBounceCount;                     */
    {SMTPMV_MAX_BOUNCE_COUNT, SMTPMV_MAX_BOUNCE_COUNT_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 26  unsigned long  BounceCount;                        */
    {SMTPMV_BOUNCE_COUNT, SMTPMV_BOUNCE_COUNT_HELP, ReadSMTPVariable,
     NULL},
    /* 27  unsigned long  MessageLimit                        */
    {SMTPMV_MESSAGE_LIMIT, SMTPMV_MESSAGE_LIMIT_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 28  unsigned long  MaximumRecipients                   */
    {SMTPMV_MAX_RECIPS, SMTPMV_MAX_RECIPS_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 29  unsigned long  MaxNullSenderRecips                 */
    {SMTPMV_MAX_NULL_SENDER_RECIPS, SMTPMV_MAX_NULL_SENDER_RECIPS_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 30  unsigned long  LocalAddress                        */
    {SMTPMV_LOCAL_IP_ADDRESS, SMTPMV_LOCAL_IP_ADDRESS_HELP,
     ReadSMTPVariable, NULL},
    /* 31  unsigned char  NMAPServer[20]                      */
    {DMCMV_NMAP_ADDRESS, DMCMV_NMAP_ADDRESS_HELP, ReadSMTPVariable, NULL},
    /* 32  unsigned char  Hostname[MAXEMAILNAMESIZE+128];     */
    {DMCMV_HOSTNAME, DMCMV_HOSTNAME_HELP, ReadSMTPVariable, NULL},
    /* 33  unsigned char  Hostaddr[MAXEMAILNAMESIZE+1];       */
    {SMTPMV_HOST_ADDRESS, SMTPMV_HOST_ADDRESS_HELP, ReadSMTPVariable,
     NULL},
    /* 34  unsigned char  Postmaster[MAXEMAILNAMESIZE+1]      */
    {DMCMV_POSTMASTER, DMCMV_POSTMASTER_HELP, ReadSMTPVariable, NULL},
    /* 35  unsigned char  OfficialName[MAXEMAILNAMESIZE+1]    */
    {DMCMV_OFFICIAL_NAME, DMCMV_OFFICIAL_NAME_HELP, ReadSMTPVariable,
     NULL},
    /* 36  unsigned char  RelayHost[MAXEMAILNAMESIZE+1]       */
    {SMTPMV_RELAY_HOST, SMTPMV_RELAY_HOST_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 37  unsigned char  **Domains                           */
    {SMTPMV_DOMAINS_ARRAY, SMTPMV_DOMAINS_ARRAY_HELP, ReadSMTPVariable,
     NULL},
    /* 38  unsigned char  **UserDomains                       */
    {SMTPMV_USER_DOMAINS_ARRAY, SMTPMV_USER_DOMAINS_ARRAY_HELP,
     ReadSMTPVariable, NULL},
    /* 39  unsigned char  **RelayDomains                      */
    {SMTPMV_RELAY_DOMAINS_ARRAY, SMTPMV_RELAY_DOMAINS_ARRAY_HELP,
     ReadSMTPVariable, NULL},
    /* 40  BOOL           SMTPReceiverStopped                 */
    {DMCMV_RECEIVER_DISABLED, DMCMV_RECEIVER_DISABLED_HELP,
     ReadSMTPVariable, WriteSMTPVariable},
    /* 41  BOOL           UseRelayHost                        */
    {SMTPMV_USE_RELAY_HOST, SMTPMV_USE_RELAY_HOST_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 42  BOOL           AllowEXPN                           */
    {SMTPMV_ALLOW_EXPN, SMTPMV_ALLOW_EXPN_HELP, ReadSMTPVariable, NULL},
    /* 43  BOOL           AllowVRFY                           */
    {SMTPMV_ALLOW_VRFY, SMTPMV_ALLOW_VRFY_HELP, ReadSMTPVariable, NULL},
    /* 44  BOOL           CheckRCPT                           */
    {SMTPMV_CHECK_RCPT, SMTPMV_CHECK_RCPT_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 45  BOOL           SendETRN                            */
    {SMTPMV_SEND_ETRN, SMTPMV_SEND_ETRN_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 46  BOOL           AcceptETRN                          */
    {SMTPMV_ACCEPT_ETRN, SMTPMV_ACCEPT_ETRN_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 47  BOOL           BlockRTSSpam                        */
    {SMTPMV_BLOCK_RTS_SPAM, SMTPMV_BLOCK_RTS_SPAM_HELP, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 48  #define        PRODUCT_VERSION                     */
    {DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadSMTPVariable, NULL},
    /* 49  XplAtomic      SMTPStats.WrongPassword             */
    {DMCMV_BAD_PASSWORD_COUNT, DMCMV_BAD_PASSWORD_COUNT, ReadSMTPVariable,
     WriteSMTPVariable},
    /* 50  unsigned char                                      */
    {DMCMV_VERSION, DMCMV_VERSION_HELP, ReadSMTPVariable, NULL},
};

static BOOL
SMTPShutdown (unsigned char *Arguments, unsigned char **Response,
              BOOL * CloseConnection)
{
    int s;
    XplThreadID id;

    if (Response) {
        if (!Arguments) {
            if (SMTPServerSocket != -1) {
                *Response = MemStrdup ("Shutting down.\r\n");
                if (*Response) {
                    id = XplSetThreadGroupID (TGid);

                    Exiting = TRUE;

                    s = SMTPServerSocket;
                    SMTPServerSocket = -1;

                    if (s != -1) {
                        IPshutdown (s, 2);
                        IPclose (s);
                    }

                    if (CloseConnection) {
                        *CloseConnection = TRUE;
                    }

                    XplSignalLocalSemaphore (SMTPShutdownSemaphore);

                    XplSetThreadGroupID (id);
                }
            }
            else if (Exiting) {
                *Response = MemStrdup ("Shutdown in progress.\r\n");
            }
            else {
                *Response = MemStrdup ("Unknown shutdown state.\r\n");
            }

            if (*Response) {
                return (TRUE);
            }

            return (FALSE);
        }

        *Response = MemStrdup ("Arguments not allowed.\r\n");
        return (TRUE);
    }

    return (FALSE);
}

static BOOL
SMTPDMCCommandHelp (unsigned char *Arguments, unsigned char **Response,
                    BOOL * CloseConnection)
{
    BOOL responded = FALSE;

    if (Response) {
        if (Arguments) {
            switch (toupper (Arguments[0])) {
            case 'M':{
                    if (XplStrCaseCmp (Arguments, DMCMC_DUMP_MEMORY_USAGE) ==
                        0) {
                        if ((*Response =
                             MemStrdup (DMCMC_DUMP_MEMORY_USAGE_HELP)) !=
                            NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                }

            case 'S':{
                    if (XplStrCaseCmp (Arguments, DMCMC_SHUTDOWN) == 0) {
                        if ((*Response =
                             MemStrdup (DMCMC_SHUTDOWN_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                    else if (XplStrCaseCmp (Arguments, DMCMC_STATS) == 0) {
                        if ((*Response =
                             MemStrdup (DMCMC_STATS_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                }

            default:{
                    break;
                }
            }
        }
        else if ((*Response = MemStrdup (DMCMC_HELP_HELP)) != NULL) {
            responded = TRUE;
        }

        if (responded
            || ((*Response = MemStrdup (DMCMC_UNKOWN_COMMAND)) != NULL)) {
            return (TRUE);
        }
    }

    return (FALSE);
}

static BOOL
SendSMTPStatistics (unsigned char *Arguments, unsigned char **Response,
                    BOOL * CloseConnection)
{
    MemStatistics poolStats;

    if (!Arguments && Response) {
        memset (&poolStats, 0, sizeof (MemStatistics));

        *Response = MemMalloc (sizeof (PRODUCT_NAME)    /*  Long Name                                                                       */
                               +sizeof (PRODUCT_SHORT_NAME)     /*  Short Name                                                                      */
                               +10      /*      PRODUCT_MAJOR_VERSION                                   */
                               + 10     /*      PRODUCT_MINOR_VERSION                                   */
                               + 10     /*      PRODUCT_LETTER_VERSION                                  */
                               + 10     /*      Connection Pool Allocation Count                */
                               + 10     /*      Connection Pool Memory Usage                    */
                               + 10     /*      Connection Pool Pitches                                 */
                               + 10     /*      Connection Pool Strikes                                 */
                               + 10     /*      DMCMV_SERVER_THREAD_COUNT                               */
                               + 10     /*      DMCMV_CONNECTION_COUNT                                  */
                               + 10     /*      DMCMV_IDLE_CONNECTION_COUNT                                     */
                               + 10     /*      DMCMV_MAX_CONNECTION_COUNT                              */
                               + 10     /*      DMCMV_QUEUE_THREAD_COUNT                                */
                               + 10     /*      DMCMV_BAD_PASSWORD_COUNT                                */
                               + 10     /*      SMTPMV_INCOMING_SERVERS                                 */
                               + 10     /*      SMTPMV_SERVICED_SERVERS                                 */
                               + 10     /*      SMTPMV_OUTGOING_SERVERS                                 */
                               + 10     /*      SMTPMV_CLOSED_OUT_SERVERS                               */
                               + 10     /*      SMTPMV_INCOMING_QUEUE_AGENT                     */
                               + 10     /*      SMTPMV_SPAM_ADDRS_BLOCKED                               */
                               + 10     /*      SMTPMV_SPAM_MAPS_BLOCKED                                */
                               + 10     /*      SMTPMV_SPAM_NO_DNS_ENTRY                                */
                               + 10     /*      SMTPMV_SPAM_DENIED_ROUTING                              */
                               + 10     /*      SMTPMV_SPAM_QUEUE                                                       */
                               + 10     /*      SMTPMV_LOCAL_MSGS_RECEIVED                              */
                               + 10     /*      SMTPMV_LOCAL_RECIP_RECEIVED                     */
                               + 10     /*      SMTPMV_LOCAL_BYTES_RECEIVED                     */
                               + 10     /*      SMTPMV_REMOTE_RECIP_RECEIVED                    */
                               + 10     /*      SMTPMV_REMOTE_RECIP_STORED                              */
                               + 10     /*      SMTPMV_REMOTE_BYTES_RECEIVED                    */
                               + 10     /*      SMTPMV_REMOTE_BYTES_STORED                              */
                               + 10     /*      SMTPMV_REMOTE_MSGS_RECEIVED                     */
                               + 10     /*      SMTPMV_REMOTE_MSGS_STORED                               */
                               + 64);   /*      Formatting      */

        MemPrivatePoolStatistics (SMTPConnectionPool, &poolStats);

        if (*Response) {
            sprintf (*Response,
                     "%s (%s: v%d.%d.%d)\r\n%lu:%lu:%lu:%lu:%d:%d:%d:%lu:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d\r\n",
                     PRODUCT_NAME, PRODUCT_SHORT_NAME, PRODUCT_MAJOR_VERSION,
                     PRODUCT_MINOR_VERSION, PRODUCT_LETTER_VERSION,
                     poolStats.totalAlloc.count, poolStats.totalAlloc.size,
                     poolStats.pitches, poolStats.strikes,
                     XplSafeRead (SMTPServerThreads),
                     XplSafeRead (SMTPConnThreads),
                     XplSafeRead (SMTPIdleConnThreads), SMTPMaxThreadLoad,
                     XplSafeRead (SMTPQueueThreads),
                     XplSafeRead (SMTPStats.WrongPassword),
                     XplSafeRead (SMTPConnThreads),
                     XplSafeRead (SMTPStats.Server.Serviced),
                     XplSafeRead (SMTPStats.Server.Outgoing),
                     XplSafeRead (SMTPStats.Server.ClosedOut),
                     XplSafeRead (SMTPStats.IncomingQueueAgent),
                     XplSafeRead (SMTPStats.SPAM.AddressBlocked),
                     XplSafeRead (SMTPStats.SPAM.MAPSBlocked),
                     XplSafeRead (SMTPStats.SPAM.NoDNSEntry),
                     XplSafeRead (SMTPStats.SPAM.DeniedRouting),
                     XplSafeRead (SMTPStats.SPAM.Queue),
                     XplSafeRead (SMTPStats.Message.Received.Local),
                     XplSafeRead (SMTPStats.Recipient.Received.Local),
                     XplSafeRead (SMTPStats.Byte.Received.Local),
                     XplSafeRead (SMTPStats.Recipient.Received.Remote),
                     XplSafeRead (SMTPStats.Recipient.Stored.Remote),
                     XplSafeRead (SMTPStats.Byte.Received.Remote),
                     XplSafeRead (SMTPStats.Byte.Stored.Remote),
                     XplSafeRead (SMTPStats.Message.Received.Remote),
                     XplSafeRead (SMTPStats.Message.Stored.Remote));

            return (TRUE);
        }

        if ((*Response = MemStrdup ("Out of memory.\r\n")) != NULL) {
            return (TRUE);
        }
    }
    else if ((Arguments)
             && ((*Response = MemStrdup ("Arguments not allowed.\r\n")) !=
                 NULL)) {
        return (TRUE);
    }

    return (FALSE);
}

static BOOL
DumpConnectionListCB (void *Buffer, void *Data)
{
    unsigned char *ptr;
    ConnectionStruct *Client = (ConnectionStruct *) Buffer;

    if (Client) {
        ptr = (unsigned char *) Data + strlen ((unsigned char *) Data);

        sprintf (ptr, "%d.%d.%d.%d:%d [%lu] \"%s\"\r\n",
                 Client->cs.sin_addr.s_net, Client->cs.sin_addr.s_host,
                 Client->cs.sin_addr.s_lh, Client->cs.sin_addr.s_impno,
                 Client->s, Client->State, Client->Command);

        return (TRUE);
    }

    return (FALSE);
}

static BOOL
DumpConnectionList (unsigned char *Arguments, unsigned char **Response,
                    BOOL * CloseConnection)
{
    if (Response) {
        if (!Arguments) {
            *Response =
                (unsigned char *) MemMalloc (XplSafeRead (SMTPConnThreads) *
                                             ((MAXEMAILNAMESIZE + BUFSIZE +
                                               64) * sizeof (unsigned char)));

            if (*Response) {
                **Response = '\0';

                MemPrivatePoolEnumerate (SMTPConnectionPool,
                                         DumpConnectionListCB,
                                         (void *) *Response);
            }
            else {
                *Response = MemStrdup ("Out of memory.\r\n");
            }
        }
        else {
            *Response = MemStrdup ("Arguments not allowed.\r\n");
        }

        if (*Response) {
            return (TRUE);
        }
    }

    return (FALSE);
}

ManagementCommands SMTPManagementCommands[] = {
    /*      0       HELP[ <SMTPCommand>]                                    */
    {DMCMC_HELP, SMTPDMCCommandHelp}
    ,
    /*      1       SHUTDOWN                                                                        */
    {DMCMC_SHUTDOWN, SMTPShutdown}
    ,
    /*      2       STATS                                                                           */
    {DMCMC_STATS, SendSMTPStatistics}
    ,
    /*      3       CONNDUMP                                                                        */
    {"ConnDump", DumpConnectionList}
    ,
/*	4	MEMORY									*/
    {DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats}
    ,
};

/*	Management Client Read Function	*/
BOOL
ReadSMTPVariable (unsigned int Variable, unsigned char *Data,
                  size_t * DataLength)
{
    int i;
    int count;
    unsigned char *ptr;

    switch (Variable) {
    case 0:{                   /*      XplAtomic               SMTPStats.IncomingQueueAgent                    */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.IncomingQueueAgent));
            }

            *DataLength = 12;
            break;
        }

    case 1:{                   /*      XplAtomic               SMTPStats.Server.Serviced                               */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Server.Serviced));
            }

            *DataLength = 12;
            break;
        }

    case 2:{                   /*      XplAtomic               SMTPStats.Server.Incoming                               */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Server.Incoming));
            }

            *DataLength = 12;
            break;
        }

    case 3:{                   /*      XplAtomic               SMTPStats.Server.ClosedOut                              */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Server.ClosedOut));
            }

            *DataLength = 12;
            break;
        }

    case 4:{                   /*      XplAtomic               SMTPStats.Server.Outgoing                               */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Server.Outgoing));
            }

            *DataLength = 12;
            break;
        }

    case 5:{                   /*      XplAtomic               SMTPStats.SPAM.AddressBlocked                   */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.SPAM.AddressBlocked));
            }

            *DataLength = 12;
            break;
        }

    case 6:{                   /*      XplAtomic               SMTPStats.SPAM.MAPSBlocked                              */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.SPAM.MAPSBlocked));
            }

            *DataLength = 12;
            break;
        }

    case 7:{                   /*      XplAtomic               SMTPStats.SPAM.NoDNSEntry                               */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.SPAM.NoDNSEntry));
            }

            *DataLength = 12;
            break;
        }

    case 8:{                   /*      XplAtomic               SMTPStats.SPAM.DeniedRouting                    */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.SPAM.DeniedRouting));
            }

            *DataLength = 12;
            break;
        }

    case 9:{                   /*      XplAtomic               SMTPStats.SPAM.Queue                                            */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.SPAM.Queue));
            }

            *DataLength = 12;
            break;
        }

    case 10:{                  /*      XplAtomic               SMTPStats.Recipient.Received.Remote     */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Recipient.Received.Remote));
            }

            *DataLength = 12;
            break;
        }

    case 11:{                  /*      XplAtomic               SMTPStats.Recipient.Received.Local      */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Recipient.Received.Local));
            }

            *DataLength = 12;
            break;
        }

    case 12:{                  /*      XplAtomic               SMTPStats.Recipient.Stored.Remote       */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Recipient.Stored.Remote));
            }

            *DataLength = 12;
            break;
        }

    case 13:{                  /*      XplAtomic               SMTPStats.Byte.Received.Remote          */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Byte.Received.Remote));
            }

            *DataLength = 12;
            break;
        }

    case 14:{                  /*      XplAtomic               SMTPStats.Byte.Received.Local                   */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Byte.Received.Local));
            }

            *DataLength = 12;
            break;
        }

    case 15:{                  /*      XplAtomic               SMTPStats.Byte.Stored.Remote                    */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Byte.Stored.Remote));
            }

            *DataLength = 12;
            break;
        }

    case 16:{                  /*      XplAtomic               SMTPStats.Message.Received.Remote       */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Message.Received.Remote));
            }

            *DataLength = 12;
            break;
        }

    case 17:{                  /*      XplAtomic               SMTPStats.Message.Received.Local                */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Message.Received.Local));
            }

            *DataLength = 12;
            break;
        }

    case 18:{                  /*      XplAtomic               SMTPStats.Message.Stored.Remote         */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.Message.Stored.Remote));
            }

            *DataLength = 12;
            break;
        }

    case 19:{                  /*      XplAtomic               SMTPServerThreads;                                              */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n", XplSafeRead (SMTPServerThreads));
            }

            *DataLength = 12;
            break;
        }

    case 20:{                  /*      XplAtomic               SMTPConnThreads;                                                        */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n", XplSafeRead (SMTPConnThreads));
            }

            *DataLength = 12;
            break;
        }

    case 21:{                  /*      XplAtomic               SMTPIdleConnThreads;                                                    */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPIdleConnThreads));
            }

            *DataLength = 12;
            break;
        }

    case 22:{                  /*      XplAtomic               SMTPQueueThreads;                                                       */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n", XplSafeRead (SMTPQueueThreads));
            }

            *DataLength = 12;
            break;
        }

    case 23:{                  /*      unsigned long   SMTPMaxThreadLoad                                                       */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010lu\r\n", SMTPMaxThreadLoad);
            }

            *DataLength = 12;
            break;
        }

    case 24:{                  /*      unsigned long   MaxMXServers                                                            */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010lu\r\n", MaxMXServers);
            }

            *DataLength = 12;
            break;
        }

    case 25:{                  /*      unsigned long   MaxBounceCount;                                                 */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010lu\r\n", MaxBounceCount);
            }

            *DataLength = 12;
            break;
        }

    case 26:{                  /*      unsigned long   BounceCount;                                                            */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010lu\r\n", BounceCount);
            }

            *DataLength = 12;
            break;
        }

    case 27:{                  /*      unsigned long   MessageLimit                                                            */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010lu\r\n", MessageLimit);
            }

            *DataLength = 12;
            break;
        }

    case 28:{                  /*      unsigned long   MaximumRecipients                                                       */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010lu\r\n", MaximumRecipients);
            }

            *DataLength = 12;
            break;
        }

    case 29:{                  /*      unsigned long   MaxNullSenderRecips                                             */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010lu\r\n", MaxNullSenderRecips);
            }

            *DataLength = 12;
            break;
        }

    case 30:{                  /*      unsigned long   LocalAddress                                                            */
            if (Data && (*DataLength > 17)) {
                *DataLength =
                    sprintf (Data, "%ld.%ld.%ld.%ld\r\n", LocalAddress & 0xFF,
                             (LocalAddress & 0xFF00) >> 8,
                             (LocalAddress & 0xFF0000) >> 16,
                             (LocalAddress & 0xFF000000) >> 24);
            }
            else {
                *DataLength = 17;
            }
            break;
        }

    case 31:{                  /*      unsigned char   NMAPServer[20]                                                          */
            count = strlen (NMAPServer) + 2;
            if (Data && ((int) (*DataLength) > count)) {
                sprintf (Data, "%s\r\n", NMAPServer);
            }

            *DataLength = count;
            break;
        }

    case 32:{                  /*      unsigned char   Hostname[MAXEMAILNAMESIZE+128];         */
            count = strlen (Hostname) + 2;
            if (Data && ((int) (*DataLength) > count)) {
                sprintf (Data, "%s\r\n", Hostname);
            }

            *DataLength = count;
            break;
        }

    case 33:{                  /*      unsigned char   Hostaddr[MAXEMAILNAMESIZE+1];                   */
            count = strlen (Hostaddr) + 2;
            if (Data && ((int) (*DataLength) > count)) {
                sprintf (Data, "%s\r\n", Hostaddr);
            }

            *DataLength = count;
            break;
        }

    case 34:{                  /*      unsigned char   Postmaster[MAXEMAILNAMESIZE+1]          */
            count = strlen (Postmaster) + 2;
            if (Data && ((int) (*DataLength) > count)) {
                sprintf (Data, "%s\r\n", Postmaster);
            }

            *DataLength = count;
            break;
        }

    case 35:{                  /*      unsigned char   OfficialName[MAXEMAILNAMESIZE+1]                */
            count = strlen (OfficialName) + 2;
            if (Data && ((int) (*DataLength) > count)) {
                sprintf (Data, "%s\r\n", OfficialName);
            }

            *DataLength = count;
            break;
        }

    case 36:{                  /*      unsigned char   RelayHost[MAXEMAILNAMESIZE+1]                   */
            count = strlen (RelayHost) + 2;
            if (Data && ((int) (*DataLength) > count)) {
                sprintf (Data, "%s\r\n", RelayHost);
            }

            *DataLength = count;
            break;
        }

    case 37:{                  /*      unsigned char   **Domains                                                                       */
            if (DomainCount) {
                if (Data
                    && ((int) (*DataLength) >
                        (DomainCount * MAXEMAILNAMESIZE))) {
                    ptr = Data;
                    for (i = 0; i < DomainCount; i++) {
                        ptr += sprintf (ptr, "%s\r\n", Domains[i]);
                    }

                    *DataLength = ptr - Data;
                }
                else {
                    *DataLength = (DomainCount * MAXEMAILNAMESIZE);
                }
            }
            else {
                if (Data && ((int) (*DataLength) > 34)) {
                    strcpy (Data, "No domains have been configured.\r\n");
                }
                *DataLength = 34;
            }
            break;
        }

    case 38:{                  /*      unsigned char   **UserDomains                                                           */
            if (UserDomainCount) {
                if (Data
                    && ((int) (*DataLength) >
                        (UserDomainCount * MAXEMAILNAMESIZE))) {
                    ptr = Data;
                    for (i = 0; i < UserDomainCount; i++) {
                        ptr += sprintf (ptr, "%s\r\n", UserDomains[i]);
                    }

                    *DataLength = ptr - Data;
                }
                else {
                    *DataLength = (UserDomainCount * MAXEMAILNAMESIZE);
                }
            }
            else {
                if (Data && ((int) (*DataLength) > 39)) {
                    strcpy (Data,
                            "No user domains have been configured.\r\n");
                }
                *DataLength = 39;
            }
            break;
        }

    case 39:{                  /*      unsigned char   **RelayDomains                                                          */
            if (RelayDomainCount) {
                if (Data
                    && ((int) (*DataLength) >
                        (RelayDomainCount * MAXEMAILNAMESIZE))) {
                    ptr = Data;
                    for (i = 0; i < RelayDomainCount; i++) {
                        ptr += sprintf (ptr, "%s\r\n", RelayDomains[i]);
                    }

                    *DataLength = ptr - Data;
                }
                else {
                    *DataLength = (RelayDomainCount * MAXEMAILNAMESIZE);
                }
            }
            else {
                if (Data && ((int) (*DataLength) > 40)) {
                    strcpy (Data,
                            "No relay domains have been configured.\r\n");
                }
                *DataLength = 40;
            }
            break;
        }

    case 40:{                  /*      BOOL                            SMTPReceiverStopped                                             */
            if (SMTPReceiverStopped == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 41:{                  /*      BOOL                            UseRelayHost                                                            */
            if (UseRelayHost == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 42:{                  /*      BOOL                            AllowEXPN                                                                       */
            if (AllowEXPN == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 43:{                  /*      BOOL                            AllowVRFY                                                                       */
            if (AllowVRFY == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 44:{                  /*      BOOL                            CheckRCPT                                                                       */
            if (CheckRCPT == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 45:{                  /*      BOOL                            SendETRN                                                                                */
            if (SendETRN == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 46:{                  /*      BOOL                            AcceptETRN                                                                      */
            if (AcceptETRN == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 47:{                  /*      BOOL                            BlockRTSSpam                                                            */
            if (BlockRTSSpam == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            }
            else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (Data && ((int) (*DataLength) > count)) {
                strcpy (Data, ptr);
            }

            *DataLength = count;
            break;
        }

    case 48:{                  /*      Version                                                                                                         */
            unsigned char version[30];

            PVCSRevisionToVersion (PRODUCT_VERSION, version);
            count = strlen (version) + 11;

            if (Data && ((int) (*DataLength) > count)) {
                ptr = Data;

                PVCSRevisionToVersion (PRODUCT_VERSION, version);
                ptr += sprintf (ptr, "smtpd.c: %s\r\n", version);

                *DataLength = ptr - Data;
            }
            else {
                *DataLength = count;
            }

            break;
        }

    case 49:{                  /*      XplAtomic               SMTPStats.WrongPassword                                 */
            if (Data && (*DataLength > 12)) {
                sprintf (Data, "%010d\r\n",
                         XplSafeRead (SMTPStats.WrongPassword));
            }

            *DataLength = 12;
            break;
        }

    case 50:{                  /*      unsigned char                                                                                                   */
            DMC_REPORT_PRODUCT_VERSION (Data, *DataLength);
            break;
        }
    }

    return (TRUE);
}

/*	Management Client Write Function	*/
BOOL
WriteSMTPVariable (unsigned int Variable, unsigned char *Data,
                   size_t DataLength)
{
    unsigned char *ptr;
    unsigned char *ptr2;
    BOOL result = TRUE;

    if (!Data || !DataLength) {
        return (FALSE);
    }

    XplRWWriteLockAcquire (&ConfigLock);

    switch (Variable) {
    case 0:{                   /*      XplAtomic               SMTPStats.IncomingQueueAgent                    */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.IncomingQueueAgent, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 1:{                   /*      XplAtomic               SMTPStats.Server.Serviced                               */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Server.Serviced, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 2:{                   /*      XplAtomic               SMTPStats.Server.Incoming                               */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Server.Incoming, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 3:{                   /*      XplAtomic               SMTPStats.Server.ClosedOut                              */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Server.ClosedOut, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 4:{                   /*      XplAtomic               SMTPStats.Server.Outgoing                               */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Server.Outgoing, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 5:{                   /*      XplAtomic               SMTPStats.SPAM.AddressBlocked                   */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.SPAM.AddressBlocked, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 6:{                   /*      XplAtomic               SMTPStats.SPAM.MAPSBlocked                              */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.SPAM.MAPSBlocked, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 7:{                   /*      XplAtomic               SMTPStats.SPAM.NoDNSEntry                               */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.SPAM.NoDNSEntry, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 8:{                   /*      XplAtomic               SMTPStats.SPAM.DeniedRouting                    */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.SPAM.DeniedRouting, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 9:{                   /*      XplAtomic               SMTPStats.SPAM.Queue                                            */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.SPAM.Queue, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 10:{                  /*      XplAtomic               SMTPStats.Recipient.Received.Remote     */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Recipient.Received.Remote, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 11:{                  /*      XplAtomic               SMTPStats.Recipient.Received.Local      */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Recipient.Received.Local, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 12:{                  /*      XplAtomic               SMTPStats.Recipient.Stored.Remote       */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Recipient.Stored.Remote, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 13:{                  /*      XplAtomic               SMTPStats.Byte.Received.Remote          */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Byte.Received.Remote, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 14:{                  /*      XplAtomic               SMTPStats.Byte.Received.Local                   */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Byte.Received.Local, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 15:{                  /*      XplAtomic               SMTPStats.Byte.Stored.Remote                    */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Byte.Stored.Remote, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 16:{                  /*      XplAtomic               SMTPStats.Message.Received.Remote       */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Message.Received.Remote, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 17:{                  /*      XplAtomic               SMTPStats.Message.Received.Local                */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Message.Received.Local, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 18:{                  /*      XplAtomic               SMTPStats.Message.Stored.Remote         */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.Message.Stored.Remote, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 23:{                  /*      unsigned long   SMTPMaxThreadLoad                                                       */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            SMTPMaxThreadLoad = atol (Data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 25:{                  /*      unsigned long   MaxBounceCount;                                                 */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            MaxBounceCount = atol (Data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 27:{                  /*      unsigned long   MessageLimit                                                            */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            MessageLimit = atol (Data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 28:{                  /*      unsigned long   MaximumRecipients                                                       */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            MaximumRecipients = atol (Data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 29:{                  /*      unsigned long   MaxNullSenderRecips                                             */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            MaxNullSenderRecips = atol (Data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 38:{                  /*      unsigned char   RelayHost[MAXEMAILNAMESIZE+1]                   */
            if (DataLength < MAXEMAILNAMESIZE) {
                strcpy (RelayHost, Data);
            }
            else {
                result = FALSE;
            }

            break;
        }

    case 44:{                  /*      BOOL                            SMTPReceiverStopped                                             */
            if ((toupper (Data[0]) == 'T') || (atol (Data) != 0)) {
                SMTPReceiverStopped = TRUE;
            }
            else if ((toupper (Data[0] == 'F')) || (atol (Data) == 0)) {
                SMTPReceiverStopped = FALSE;
            }
            else {
                result = FALSE;
            }

            break;
        }

    case 45:{                  /*      BOOL                            UseRelayHost                                                            */
            if ((toupper (Data[0]) == 'T') || (atol (Data) != 0)) {
                UseRelayHost = TRUE;
            }
            else if ((toupper (Data[0] == 'F')) || (atol (Data) == 0)) {
                UseRelayHost = FALSE;
            }
            else {
                result = FALSE;
            }

            break;
        }

    case 48:{                  /*      BOOL                            CheckRCPT                                                                       */
            if ((toupper (Data[0]) == 'T') || (atol (Data) != 0)) {
                CheckRCPT = TRUE;
            }
            else if ((toupper (Data[0] == 'F')) || (atol (Data) == 0)) {
                CheckRCPT = FALSE;
            }
            else {
                result = FALSE;
            }

            break;
        }

    case 49:{                  /*      BOOL                            SendETRN                                                                                */
            if ((toupper (Data[0]) == 'T') || (atol (Data) != 0)) {
                SendETRN = TRUE;
            }
            else if ((toupper (Data[0] == 'F')) || (atol (Data) == 0)) {
                SendETRN = FALSE;
            }
            else {
                result = FALSE;
            }

            break;
        }

    case 50:{                  /*      BOOL                            AcceptETRN                                                                      */
            if ((toupper (Data[0]) == 'T') || (atol (Data) != 0)) {
                AcceptETRN = TRUE;
            }
            else if ((toupper (Data[0] == 'F')) || (atol (Data) == 0)) {
                AcceptETRN = FALSE;
            }
            else {
                result = FALSE;
            }

            break;
        }

    case 51:{                  /*      BOOL                            BlockRTSSpam                                                            */
            if ((toupper (Data[0]) == 'T') || (atol (Data) != 0)) {
                BlockRTSSpam = TRUE;
            }
            else if ((toupper (Data[0] == 'F')) || (atol (Data) == 0)) {
                BlockRTSSpam = FALSE;
            }
            else {
                result = FALSE;
            }

            break;
        }

    case 53:{                  /*      XplAtomic               SMTPStats.WrongPassword                                 */
            ptr = strchr (Data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr (Data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite (SMTPStats.WrongPassword, atol (Data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

    case 19:                   /*      XplAtomic               SMTPServerThreads;                                              */
    case 20:                   /*      XplAtomic               SMTPConnThreads;                                                        */
    case 21:                   /*      XplAtomic               SMTPIdleConnThreads;                                                    */
    case 22:                   /*      XplAtomic               SMTPQueueThreads;                                                       */
    case 24:                   /*      unsigned long   MaxMXServers                                                            */
    case 26:                   /*      unsigned long   BounceCount;                                                            */
    case 30:                   /*      unsigned long   LocalAddress                                                            */
    case 33:                   /*      unsigned char   NMAPServer[20]                                                          */
    case 34:                   /*      unsigned char   Hostname[MAXEMAILNAMESIZE+128];         */
    case 35:                   /*      unsigned char   Hostaddr[MAXEMAILNAMESIZE+1];                   */
    case 36:                   /*      unsigned char   Postmaster[MAXEMAILNAMESIZE+1]          */
    case 37:                   /*      unsigned char   OfficialName[MAXEMAILNAMESIZE+1]                */
    case 39:                   /*      unsigned char   **Domains                                                                       */
    case 40:                   /*      unsigned char   **UserDomains                                                           */
    case 41:                   /*      unsigned char   **RelayDomains                                                          */
    case 46:                   /*      BOOL                            AllowEXPN                                                                       */
    case 47:                   /*      BOOL                            AllowVRFY                                                                       */
    case 52:                   /*      #define                 PRODUCT_VERSION                                                 */
    case 54:                   /*      unsigned char                                                                                                   */
    default:{
            result = FALSE;
            break;
        }
    }

    XplRWWriteLockRelease (&ConfigLock);

    return (result);
}

static BOOL
SMTPConnectionAllocCB (void *Buffer, void *ClientData)
{
    register ConnectionStruct *c = (ConnectionStruct *) Buffer;

    memset (c, 0, sizeof (ConnectionStruct));
    c->s = -1;
    c->NMAPs = -1;
    c->State = STATE_FRESH;
    c->MsgFlags = MSG_FLAG_ENCODING_NONE;

    return (TRUE);
}

static void
ReturnSMTPConnection (ConnectionStruct * Client)
{
    register ConnectionStruct *c = Client;

    memset (c, 0, sizeof (ConnectionStruct));
    c->s = -1;
    c->NMAPs = -1;
    c->State = STATE_FRESH;
    c->MsgFlags = MSG_FLAG_ENCODING_NONE;


    MemPrivatePoolReturnEntry (c);

    return;
}

__inline static unsigned char *
strchrRN (unsigned char *Buffer, unsigned char SrchChar,
          unsigned char *EndPtr)
{
    register unsigned char *ptr = Buffer;
    register unsigned char srchChar = SrchChar;

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


#define	SocketReadyTimeout(Socket, Timeout, Exiting)					\
	{																					\
		int                  ret;												\
		fd_set               readfds;											\
		struct timeval       timeout;											\
																						\
		FD_ZERO(&readfds);														\
		FD_SET((Socket), &readfds);											\
		timeout.tv_usec = 0;														\
		timeout.tv_sec = (Timeout);											\
		ret = select(FD_SETSIZE, &readfds, NULL, NULL, &timeout);	\
																						\
		if (Exiting) {																\
			return(0);																\
		}																				\
																						\
		if (ret < 1) {																\
			return(-1);																\
		}																				\
	}


int
ClientRead (ConnectionStruct * Client, unsigned char *Buf, int Len, int Flags)
{
#ifndef WIN32
    int ret;
    struct pollfd pfd;

    pfd.fd = Client->s;
    pfd.events = POLLIN;
    ret = poll (&pfd, 1, SocketTimeout * 1000);
    if (ret > 0) {
        if (!(pfd.revents & (POLLERR | POLLHUP | POLLNVAL))) {
            do {
                ret = IPrecv (Client->s, Buf, Len, 0);
                if (ret >= 0) {
                    break;
                }
                else if (errno == EINTR) {
                    continue;
                }

                ret = -1;
                break;
            } while (TRUE);
        }
        else {
            ret = -1;
        }
    }
    else {
        ret = -1;
    }

    if (Exiting) {
        return (0);
    }

    if (ret < 1) {
        return (-1);
    }

    return (ret);
#else
    SocketReadyTimeout (Client->s, SocketTimeout * 1000, Exiting);
    return (IPrecv (Client->s, (unsigned char *) Buf, Len, 0));
#endif
}

int
ClientWrite (ConnectionStruct * Client, unsigned char *Buf, int Len,
             int Flags)
{
    return (IPsend (Client->s, Buf, Len, Flags));
}

int
ClientReadSSL (ConnectionStruct * Client, unsigned char *Buf, int Len,
               int Flags)
{
    int llen;

    llen = SSL_read (Client->CSSL, (void *) Buf, Len);

    if (!Exiting) {
        return (llen);
    }
    else {
        return (-1);
    }
}

int
ClientWriteSSL (ConnectionStruct * Client, unsigned char *Buf, int Len,
                int Flags)
{
    return (SSL_write (Client->CSSL, (void *) Buf, Len));
}

int
NMAPRead (ConnectionStruct * Client, unsigned char *Buf, int Len, int Flags)
{
#ifndef WIN32
    int ret;
    struct pollfd pfd;

    pfd.fd = Client->NMAPs;
    pfd.events = POLLIN;
    ret = poll (&pfd, 1, SocketTimeout * 1000);
    if (ret > 0) {
        if (!(pfd.revents & (POLLERR | POLLHUP | POLLNVAL))) {
            do {
                ret = recv (Client->NMAPs, Buf, Len, 0);
                if (ret >= 0) {
                    break;
                }
                else if (errno == EINTR) {
                    continue;
                }

                ret = -1;
                break;
            } while (TRUE);
        }
        else {
            ret = -1;
        }
    }
    else {
        ret = -1;
    }

    if (Exiting) {
        return (0);
    }

    if (ret < 1) {
        return (-1);
    }

    return (ret);
#else

    SocketReadyTimeout (Client->NMAPs, SocketTimeout * 1000, Exiting);
    return (IPrecv (Client->NMAPs, (unsigned char *) Buf, Len, 0));
#endif
}

int
NMAPWrite (ConnectionStruct * Client, unsigned char *Buf, int Len, int Flags)
{
    return (IPsend (Client->NMAPs, Buf, Len, Flags));
}

int
NMAPReadSSL (ConnectionStruct * Client, unsigned char *Buf, int Len,
             int Flags)
{
    int llen;

    llen = SSL_read (Client->NSSL, (void *) Buf, Len);

    if (!Exiting) {
        return (llen);
    }
    else {
        return (-1);
    }
}

int
NMAPWriteSSL (ConnectionStruct * Client, unsigned char *Buf, int Len,
              int Flags)
{
    int err;

    err = SSL_write (Client->NSSL, (void *) Buf, Len);
    if (err == -1) {
        return (-1);
    }

    return (Len);
}

BOOL
EndClientConnection (ConnectionStruct * Client)
{
    CMDisconnected (Client->cs.sin_addr.s_addr);

    if (Client) {
        if (Client->State == STATE_ENDING) {
            return (TRUE);
        }

        if (Client->State >= STATE_WAITING) {
            if (Client->State == STATE_WAITING) {
                XplSafeDecrement (SMTPStats.IncomingQueueAgent);
            }
        }
        else {
            XplSafeIncrement (SMTPStats.Server.Serviced);
        }

        Client->Flags = Client->State;  /* abusing the field - saves stack */
        Client->State = STATE_ENDING;

        if (Client->From) {
            MemFree (Client->From);
            Client->From = NULL;
        }
        if (Client->NMAPs != -1) {
            if (Client->Flags != STATE_HELO) {
                /* if smtp is in the middle of receiving a message,   */
                /* we need to send a dot on a line by itself          */
                /* to get NMAP out of the receiving state and into    */
                /* the command state so we can issue the QABRT and    */
                /* QUIT commands.  If NMAP is already in the command  */
                /* state, the dot on a line by itself will not hurt   */
                /* anything.  NMAP will just send 'unknown command'   */
                SendNMAPServer (Client, "\r\n.\r\n", 5);
                SendNMAPServer (Client, "QABRT\r\nQUIT\r\n", 13);
            }
            else {
                SendNMAPServer (Client, "QUIT\r\n", 6);
            }
            IPclose (Client->NMAPs);
            Client->NMAPs = -1;
        }

        if (Client->s != -1) {
            FlushClient (Client);
            IPclose (Client->s);
        }

        if (Client->CSSL) {
            SSL_shutdown (Client->CSSL);
            SSL_free (Client->CSSL);
            Client->CSSL = NULL;
        }

        if (Client->NSSL) {
            SSL_shutdown (Client->NSSL);
            SSL_free (Client->NSSL);
            Client->NSSL = NULL;
        }

        if (Client->AuthFrom != NULL) {
            MemFree (Client->AuthFrom);
            Client->AuthFrom = NULL;
        }

        /* Bump our thread count */
        if (Client->Flags < STATE_WAITING) {
            XplSafeDecrement (SMTPConnThreads);
        }
        else {
            XplSafeDecrement (SMTPQueueThreads);
        }

        ReturnSMTPConnection (Client);
    }
    XplExitThread (TSR_THREAD, 0);
    return (FALSE);
}

BOOL
FlushClient (ConnectionStruct * Client)
{
    int count;
    unsigned long sent = 0;

    while ((sent < Client->SBufferPtr) && (!Exiting)) {
        count =
            DoClientWrite (Client, Client->SBuffer + sent,
                           Client->SBufferPtr - sent, 0);
        if ((count < 1) || Exiting) {
            return (EndClientConnection (Client));
        }
        sent += count;
    }

    if (!Exiting) {
        Client->SBufferPtr = 0;
        Client->SBuffer[0] = '\0';
        return (TRUE);
    }

    return (EndClientConnection (Client));
}

static BOOL
SendClient (ConnectionStruct * Client, unsigned char *Data, int Len)
{
    int sent = 0;

    if (!Len)
        return (TRUE);

    if (!Data)
        return (FALSE);

    while (sent < Len) {
        if (Len - sent + Client->SBufferPtr >= MTU) {
            memcpy (Client->SBuffer + Client->SBufferPtr, Data + sent,
                    (MTU - Client->SBufferPtr));
            sent += (MTU - Client->SBufferPtr);
            Client->SBufferPtr += (MTU - Client->SBufferPtr);
            if (!FlushClient (Client)) {
                return (FALSE);
            }
        }
        else {
            memcpy (Client->SBuffer + Client->SBufferPtr, Data + sent,
                    Len - sent);
            Client->SBufferPtr += Len - sent;
            sent = Len;
        }
    }
    Client->SBuffer[Client->SBufferPtr] = '\0';
    return (TRUE);
}

BOOL
SendNMAPServer (ConnectionStruct * Client, unsigned char *Data, int Len)
{
    int count;
    int sent = 0;

    while (sent < Len) {
        count = DoNMAPWrite (Client, Data + sent, Len - sent, 0);
        if ((count < 1) || (Exiting)) {
            if (!Exiting) {
                IPclose (Client->NMAPs);
                Client->NMAPs = -1;
            }
            if (!(Client->State == STATE_ENDING))
                return (EndClientConnection (Client));
            else
                return (FALSE);
        }
        sent += count;
    }

    return (TRUE);
}

int
GetNMAPAnswer (ConnectionStruct * Client, unsigned char *Reply,
               unsigned long ReplyLen, BOOL CheckForResult)
{
    BOOL Ready = FALSE;
    int count;
    int Result;
    unsigned char *ptr;

    if (Client->NMAPs == -1)
        return (-1);

    if ((Client->NBufferPtr > 0)
        && ((ptr = strchr (Client->NBuffer, 0x0a)) != NULL)) {
        *ptr = '\0';
        if (ReplyLen > strlen (Client->NBuffer)) {
            strcpy (Reply, Client->NBuffer);
        }
        else {
            strncpy (Reply, Client->NBuffer, ReplyLen - 1);
            Reply[ReplyLen - 1] = '\0';
        }
        Client->NBufferPtr = strlen (ptr + 1);
        memmove (Client->NBuffer, ptr + 1, Client->NBufferPtr + 1);
        if ((ptr = strrchr (Reply, 0x0d)) != NULL)
            *ptr = '\0';
        Ready = TRUE;
    }
    else {
        while (!Ready) {
            if (Exiting) {
                return (EndClientConnection (Client));
            }
            count =
                DoNMAPRead (Client, Client->NBuffer + Client->NBufferPtr,
                            BUFSIZE - Client->NBufferPtr, 0);
            if ((count < 1) || (Exiting)) {
                return (EndClientConnection (Client));
            }
            Client->NBufferPtr += count;
            Client->NBuffer[Client->NBufferPtr] = '\0';
            if ((ptr = strchr (Client->NBuffer, 0x0a)) != NULL) {
                *ptr = '\0';
                count =
                    min ((unsigned long) (ptr - Client->NBuffer) + 1,
                         ReplyLen);
                memcpy (Reply, Client->NBuffer, count);

                Client->NBufferPtr = strlen (ptr + 1);
                memmove (Client->NBuffer, ptr + 1, Client->NBufferPtr + 1);
                if ((ptr = strrchr (Reply, 0x0d)) != NULL)
                    *ptr = '\0';
                Ready = TRUE;
            }
        }
    }
    if (CheckForResult) {
        if ((Reply[4] != ' ') && (Reply[4] != '-')) {
            ptr = strchr (Reply, ' ');
            if (!ptr) {
                return (-1);
            }
        }
        else {
            ptr = Reply + 4;
        }
        *ptr = '\0';
        Result = atoi (Reply);
        if (Result == 5001) {
            IPclose (Client->NMAPs);
            Client->NMAPs = -1;
        }
        memmove (Reply, ptr + 1, strlen (ptr + 1) + 1);
    }
    else {
        Result = atoi (Reply);
    }
    return (Result);
}

static BOOL
GetClientAnswer (ConnectionStruct * Client)
{
    BOOL Ready = FALSE;
    unsigned char *ptr;
    unsigned char Answer[BUFSIZE + 1];
    int count;
    unsigned long lineLen;

    if ((Client->BufferPtr > 0)
        &&
        ((ptr =
          strchrRN (Client->Buffer, 0x0a,
                    Client->Buffer + Client->BufferPtr)) != NULL)) {
        *ptr = '\0';
        lineLen = ptr - Client->Buffer + 1;
        memcpy (Client->Command, Client->Buffer, lineLen);
        Client->BufferPtr -= lineLen;
        memmove (Client->Buffer, ptr + 1, Client->BufferPtr + 1);
        if ((ptr = strrchr (Client->Command, 0x0d)) != NULL)
            *ptr = '\0';
        Ready = TRUE;
    }
    else {
        while (!Ready) {
            if (Exiting) {
                snprintf (Answer, sizeof (Answer), "421 %s %s\r\n", Hostname,
                          MSG421SHUTDOWN);
                SendClient (Client, Answer, strlen (Answer));
                return (EndClientConnection (Client));
            }
            count =
                DoClientRead (Client, Client->Buffer + Client->BufferPtr,
                              BUFSIZE - Client->BufferPtr, 0);
            if (count < 1) {
                if (Exiting) {
                    snprintf (Answer, sizeof (Answer), "421 %s %s\r\n",
                              Hostname, MSG421SHUTDOWN);
                    SendClient (Client, Answer, strlen (Answer));
                }
                return (EndClientConnection (Client));
            }
            Client->BufferPtr += count;
            Client->Buffer[Client->BufferPtr] = '\0';
            if ((ptr =
                 strchrRN (Client->Buffer, 0x0a,
                           Client->Buffer + Client->BufferPtr)) != NULL) {
                *ptr = '\0';
                lineLen = ptr - Client->Buffer + 1;
                memcpy (Client->Command, Client->Buffer, lineLen);
                Client->BufferPtr -= lineLen;
                memmove (Client->Buffer, ptr + 1, Client->BufferPtr);
                Client->Buffer[Client->BufferPtr] = '\0';
                if ((ptr = strrchr (Client->Command, 0x0d)) != NULL)
                    *ptr = '\0';
                Ready = TRUE;
            }
        }
    }
    return (TRUE);
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
    BOOL AllowAuth = TRUE;
    BOOL NullSender = FALSE;
    BOOL TooManyNullSenderRecips = FALSE;
    BOOL RequireAuth = FALSE;
    unsigned char *ptr, *ptr2;
    unsigned char Answer[BUFSIZE + 1];
    unsigned char Reply[BUFSIZE + 1];
    struct sockaddr_in soc_address;
    struct sockaddr_in *sin = &soc_address;
    time_t connectionTime;
    unsigned long lineLen;

    time (&connectionTime);

    if (Client->ClientSSL) {
        LogMsg (LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_SSL_CONNECTION,
                LOG_INFO, "SSL connection %d",
                XplHostToLittle (Client->cs.sin_addr.s_addr));
        if (SSL_accept (Client->CSSL) != 1) {
            return (EndClientConnection (Client));
        }
    }
    else {
        LogMsg (LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_CONNECTION,
                LOG_INFO, "Connection %d",
                XplHostToLittle (Client->cs.sin_addr.s_addr));
    }

    XplRWReadLockAcquire (&ConfigLock);
    if (UBEConfig & UBE_DISABLE_AUTH) {
        AllowAuth = FALSE;
    }

    ReplyInt =
        CMVerifyConnect (Client->cs.sin_addr.s_addr, Answer, &RequireAuth);
    if (ReplyInt == CM_RESULT_DENY_PERMANENT) {
        /* We don't like the guy */
        XplRWReadLockRelease (&ConfigLock);
        LogMsg (LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_CONNECTION_BLOCKED,
                LOG_INFO, "Connection blocked %d",
                XplHostToLittle (Client->cs.sin_addr.s_addr));
        XplSafeIncrement (SMTPStats.SPAM.AddressBlocked);

        if (Answer[0] != '\0') {
            SendClient (Client, MSG553COMMENT, MSG553COMMENT_LEN);
            SendClient (Client, Answer, strlen (Answer));
            SendClient (Client, "\r\n", 2);
        }
        else {
            SendClient (Client, MSG550SPAMBLOCK, MSG550SPAMBLOCK_LEN);
        }
        FlushClient (Client);
        return (EndClientConnection (Client));
    }
    else if (ReplyInt != CM_RESULT_ALLOWED) {
        /* Either we don't like the guy, or there was an error */
        XplRWReadLockRelease (&ConfigLock);
        LogMsg (LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_CONNECTION_BLOCKED,
                LOG_INFO, "Connection blocked %d Blocklist: %d",
                XplHostToLittle (Client->cs.sin_addr.s_addr),
                LOGGING_BLOCK_BLOCKLIST);
        XplSafeIncrement (SMTPStats.SPAM.AddressBlocked);

        if (Answer[0] != '\0') {
            SendClient (Client, MSG453COMMENT, MSG453COMMENT_LEN);
            SendClient (Client, Answer, strlen (Answer));
            SendClient (Client, "\r\n", 2);
        }
        else {
            SendClient (Client, MSG453TRYLATER, MSG453TRYLATER_LEN);
        }
        FlushClient (Client);
        return (EndClientConnection (Client));
    }

    if (UBEConfig & UBE_DEFAULT_NOT_TRUSTED) {
        IsTrusted = FALSE;

        if (UBEConfig & UBE_SMTP_AFTER_POP) {
            ReplyInt = CMVerifyRelay (Client->cs.sin_addr.s_addr, Answer);
            if (ReplyInt == CM_RESULT_ALLOWED) {
                if (Answer[0] != '\0') {
                    if (Client->AuthFrom == NULL) {
                        Client->AuthFrom = MemStrdup (Answer);
                        IsTrusted = TRUE;
                    }
                    else {
                        MemFree (Client->AuthFrom);
                        Client->AuthFrom = MemStrdup (Answer);
                        IsTrusted = TRUE;
                    }
                }
                else {
                    IsTrusted = TRUE;
                }
            }
        }
    }

    XplRWReadLockRelease (&ConfigLock);

    /* Connect to NMAP server */
    sin->sin_addr.s_addr=inet_addr(NMAPServer);
    sin->sin_family=AF_INET;
    sin->sin_port=htons(BONGO_QUEUE_PORT);
    Client->NMAPs=IPsocket(PF_INET, SOCK_STREAM, IPPROTO_TCP);

    ReplyInt =
        IPconnect (Client->NMAPs, (struct sockaddr *) &soc_address,
                   sizeof (soc_address));
    if (ReplyInt) {
        IPclose (Client->NMAPs);
        Client->NMAPs = -1;
        count =
            snprintf (Reply, sizeof (Reply), "421 %s %s\r\n", Hostname,
                      MSG421SHUTDOWN);
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE,
                LOG_ERROR, "NMAP unavailable %d Reply: %d",
                XplHostToLittle (soc_address.sin_addr.s_addr), ReplyInt);
        SendClient (Client, Reply, count);
        FlushClient (Client);
        return (EndClientConnection (Client));
    }

    ReplyInt = GetNMAPAnswer (Client, Answer, sizeof (Answer), TRUE);

    switch (ReplyInt) {
    case NMAP_READY:{
            break;
        }

    case 4242:{
            unsigned char *ptr, *salt;
            MD5_CTX mdContext;
            unsigned char digest[16];
            unsigned char HexDigest[33];
            int i;

            ptr = strchr (Answer, '<');
            if (ptr) {
                ptr++;
                salt = ptr;
                if ((ptr = strchr (ptr, '>')) != NULL) {
                    *ptr = '\0';
                }

                MD5_Init(&mdContext);
                MD5_Update(&mdContext, salt, strlen(salt));
                MD5_Update(&mdContext, NMAPHash, NMAP_HASH_SIZE);
                MD5_Final(digest, &mdContext);
                for (i=0; i<16; i++) {
                    sprintf(HexDigest+(i*2),"%02x",digest[i]);
                }
                ReplyInt = sprintf(Answer, "AUTH SYSTEM %s\r\n", HexDigest);

                SendNMAPServer (Client, Answer, ReplyInt);
                if (GetNMAPAnswer (Client, Answer, sizeof (Answer), TRUE) ==
                    1000) {
                    break;
                }
            }
            /* Fall-through */
        }

    default:{
            LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_UNAVAILABLE,
                    LOG_ERROR, "NMAP unavailable %d Reply: %d",
                    XplHostToLittle (soc_address.sin_addr.s_addr), ReplyInt);
            IPclose (Client->NMAPs);
            Client->NMAPs = -1;
            snprintf (Reply, sizeof (Reply), "421 %s %s (%d)\r\n", Hostname,
                      MSG421SHUTDOWN, ReplyInt);
            SendClient (Client, Reply, strlen (Reply));
            FlushClient (Client);
            return (EndClientConnection (Client));
        }
    }

    snprintf (Answer, sizeof (Answer), "220 %s %s %s\r\n", Hostname,
              MSG220READY, PRODUCT_VERSION);
    SendClient (Client, Answer, strlen (Answer));
    FlushClient (Client);

    while (Working) {
        Ready = FALSE;
        if ((Client->BufferPtr > 0)
            &&
            ((ptr =
              strchrRN (Client->Buffer, 0x0a,
                        Client->Buffer + Client->BufferPtr)) != NULL)) {
            *ptr = '\0';
            lineLen = ptr - Client->Buffer + 1;
            memcpy (Client->Command, Client->Buffer, lineLen);
            Client->BufferPtr -= lineLen;
            memmove (Client->Buffer, ptr + 1, Client->BufferPtr + 1);
            if ((ptr = strrchr (Client->Command, 0x0d)) != NULL)
                *ptr = '\0';
            Ready = TRUE;
        }
        else {
            while (!Ready) {
                if (Exiting == FALSE) {
                    count =
                        DoClientRead (Client,
                                      Client->Buffer + Client->BufferPtr,
                                      BUFSIZE - Client->BufferPtr, 0);
                }
                else {
                    snprintf (Answer, sizeof (Answer), "421 %s %s\r\n",
                              Hostname, MSG421SHUTDOWN);
                    SendClient (Client, Answer, strlen (Answer));
                    return (EndClientConnection (Client));
                }

                if (count > 0) {
                    Client->BufferPtr += count;
                    Client->Buffer[Client->BufferPtr] = '\0';
                }
                else if (Exiting == FALSE) {
                    LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                            LOGGER_EVENT_CONNECTION_TIMEOUT,
                            LOG_ERROR,
                            "Connection timeout %d Time: %d",
                            XplHostToLittle (soc_address.sin_addr.s_addr),
                            time (NULL) - connectionTime);
                    return (EndClientConnection (Client));
                }
                else {
                    snprintf (Answer, sizeof (Answer), "421 %s %s\r\n",
                              Hostname, MSG421SHUTDOWN);
                    SendClient (Client, Answer, strlen (Answer));
                    return (EndClientConnection (Client));
                }

                if ((ptr =
                     strchrRN (Client->Buffer, 0x0a,
                               Client->Buffer + Client->BufferPtr)) != NULL) {
                    *ptr = '\0';
                    lineLen = ptr - Client->Buffer + 1;
                    memcpy (Client->Command, Client->Buffer, lineLen);
                    Client->BufferPtr -= lineLen;
                    memmove (Client->Buffer, ptr + 1, Client->BufferPtr);
                    Client->Buffer[Client->BufferPtr] = '\0';
                    if ((ptr = strrchr (Client->Command, 0x0d)) != NULL)
                        *ptr = '\0';
                    Ready = TRUE;
                }
            }
        }

#if 0
        XplConsolePrintf ("\rGot command:%s %s\n", Client->Command,
                          Client->ClientSSL ? "(Secure)" : "");
#endif
        switch (toupper (Client->Command[0])) {
        case 'H':
            switch (toupper (Client->Command[3])) {
            case 'O':          /* HELO */
                if (Client->State == STATE_FRESH) {
                    snprintf (Answer, sizeof (Answer), "250 %s %s\r\n",
                              Hostname, MSG250HELLO);
                    SendClient (Client, Answer, strlen (Answer));
                    strncpy (Client->RemoteHost, Client->Command + 5,
                             sizeof (Client->RemoteHost));
                    Client->State = STATE_HELO;

                    NullSender = FALSE;
                    TooManyNullSenderRecips = FALSE;
                }
                else {
                    SendClient (Client, MSG503BADORDER, MSG503BADORDER_LEN);
                }
                break;

            case 'P':          /* HELP */
                SendClient (Client, MSG211HELP, MSG211HELP_LEN);
                break;

            default:
                SendClient (Client, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                break;
            }
            break;

        case 'S':{             /* SAML, SOML, SEND */
                switch (toupper (Client->Command[1])) {
                case 'T':{     /* STARTTLS */
                        if (!AllowClientSSL) {
                            SendClient (Client, MSG500UNKNOWN,
                                        MSG500UNKNOWN_LEN);
                            break;
                        }

                        SendClient (Client, MSG220TLSREADY,
                                    MSG220TLSREADY_LEN);
                        FlushClient (Client);
                        Client->ClientSSL = TRUE;
                        Client->CSSL = SSL_new (SSLContext);
                        if (Client->CSSL) {
                            if ((SSL_set_bsdfd (Client->CSSL, Client->s) == 1)
                                && (SSL_accept (Client->CSSL) == 1)) {
                                Client->State = STATE_FRESH;
                                break;
                            }
                            SSL_free (Client->CSSL);
                            Client->CSSL = NULL;
                        }
                        Client->ClientSSL = FALSE;
                        Client->State = STATE_FRESH;
                        break;
                    }

                case 'A':      /* SAML */
                case 'O':      /* SOML */
                case 'E':{     /* SEND */
                        SendClient (Client, MSG502NOTIMPLEMENTED,
                                    MSG502NOTIMPLEMENTED_LEN);
                        break;
                    }

                default:{
                        SendClient (Client, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                        break;
                    }
                }
                break;
            }

        case 'A':{             /* AUTH */
                unsigned char *PW;
                MDBValueStruct *User;

                if (AllowAuth == FALSE) {
                    SendClient (Client, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                    break;
                }

                if (XplStrNCaseCmp (Client->Command + 5, "LOGIN", 5) != 0) {
                    SendClient (Client, MSG504BADAUTH, MSG504BADAUTH_LEN);
                    break;
                }
                if (!isspace (Client->Command[10])) {
                    SendClient (Client, "334 VXNlcm5hbWU6\r\n", 18);
                    FlushClient (Client);
                    GetClientAnswer (Client);
                    strcpy (Reply, Client->Command);
                }
                else {
                    strcpy (Reply, Client->Command + 11);
                }

                SendClient (Client, "334 UGFzc3dvcmQ6\r\n", 18);
                FlushClient (Client);

                GetClientAnswer (Client);

                if (Client->Command[0] == '*' && Client->Command[1] == '\0') {
                    SendClient (Client,
                                "501 Authentication aborted by client\r\n",
                                38);
                    break;
                }

                PW = DecodeBase64 (Client->Command);
                DecodeBase64 (Reply);

                User = MDBCreateValueStruct (SMTPDirectoryHandle, NULL);
                if (MsgFindObject (Reply, Answer, NULL, NULL, User)) {
                    if (!MDBVerifyPassword (Answer, PW, User)) {
                        SendClient (Client, "501 Authentication failed!\r\n",
                                    28);
                        LogMsg (LOGGER_SUBSYSTEM_AUTH,
                                LOGGER_EVENT_WRONG_PASSWORD, LOG_NOTICE,
                                "Wrong password: %s/%s %d",
                                User->Used ? User->Value[0] : Reply, PW,
                                XplHostToLittle (Client->cs.sin_addr.s_addr));
                        XplSafeIncrement (SMTPStats.WrongPassword);
                    }
                    else {
                        LogMsg (LOGGER_SUBSYSTEM_AUTH,
                                LOGGER_EVENT_LOGIN,
                                LOG_INFO,
                                "Login %s %d",
                                User->Value[0],
                                XplHostToLittle (Client->cs.sin_addr.s_addr));
                        if (Client->AuthFrom == NULL) {
                            Client->AuthFrom = MemStrdup (User->Value[0]);
                            Client->State = STATE_AUTH;
                            SendClient (Client,
                                        "235 Authentication successful!\r\n",
                                        32);
                            IsTrusted = TRUE;
                        }
                        else {
                            MemFree (Client->AuthFrom);
                            Client->AuthFrom = MemStrdup (User->Value[0]);
                            Client->State = STATE_AUTH;
                            SendClient (Client,
                                        "235 Authentication successful!\r\n",
                                        32);
                            IsTrusted = TRUE;
                        }
                    }
                }
                else {
                    LogMsg (LOGGER_SUBSYSTEM_AUTH,
                            LOGGER_EVENT_UNKNOWN_USER,
                            LOG_NOTICE,
                            "Unknown user %s %s %d",
                            Reply,
                            PW, XplHostToLittle (Client->cs.sin_addr.s_addr));
                    SendClient (Client, "501 Authentication failed!\r\n", 28);
                }
                MDBDestroyValueStruct (User);
                break;
            }

        case 'R':
            switch (toupper (Client->Command[1])) {
            case 'S':          /* RSET */
                if (Client->State != STATE_FRESH) {
                    SendNMAPServer (Client, "QABRT\r\n", 7);
                    GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);
                }
                Client->State = STATE_FRESH;
                Client->Flags = 0;
                Client->RecipCount = 0;
                Client->MsgFlags = MSG_FLAG_ENCODING_NONE;
                if (Client->From) {
                    MemFree (Client->From);
                    Client->From = NULL;
                }

                SendClient (Client, MSG250OK, MSG250OK_LEN);
                break;


            case 'C':{         /* RCPT TO */
                    unsigned char temp, *name;
                    BOOL GotFlags = FALSE;
                    unsigned char To[MAXEMAILNAMESIZE + 1] = "";
                    unsigned char *Orcpt = NULL;

                    if (Client->State < STATE_FROM) {
                        SendClient (Client, MSG501NOSENDER,
                                    MSG501NOSENDER_LEN);
                        break;
                    }

                    if ((ptr = strchr (Client->Command, ':')) == NULL) {
                        SendClient (Client, MSG501SYNTAX, MSG501SYNTAX_LEN);
                        break;
                    }

                    if (TooManyNullSenderRecips == TRUE) {
                        XplDelay (250);

                        Client->RecipCount++;

                        SendClient (Client, MSG550TOOMANY, MSG550TOOMANY_LEN);

                        XplSafeIncrement (SMTPStats.SPAM.DeniedRouting);
                        break;
                    }

                    if ((ptr2 = strchr (ptr + 1, '<')) != NULL) {
                        ptr = ptr2 + 1;
                        if (*ptr == '\0') {
                            SendClient (Client, MSG501SYNTAX,
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
                            SendClient (Client, MSG501SYNTAX,
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

                    if (temp != '\0')
                        do {
                            ptr2++;
                        } while (isspace (*ptr2));

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
                                    SendClient (Client, MSG501PARAMERROR,
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
                                SendClient (Client, MSG501PARAMERROR,
                                            MSG501PARAMERROR_LEN);
                                goto QuitRcpt;
                            }

                            *ptr2 = temp;
                            if (!Orcpt) {
                                SendClient (Client, MSG451INTERNALERR,
                                            MSG451INTERNALERR_LEN);
                                goto QuitRcpt;
                            }
                            break;

                        default:
                            SendClient (Client, MSG501PARAMERROR,
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
                        SendClient (Client, MSG501RECIPNO, MSG501RECIPNO_LEN);
                        break;
                    }
                    ptr = strchr (name, '@');
                    if (!ptr && ReplyInt > MAX_USERPART_LEN) {
                        SendClient (Client, MSG501RECIPNO, MSG501RECIPNO_LEN);
                        break;
                    }
                    else if (ptr && ((ptr - name) > MAX_USERPART_LEN)) {
                        SendClient (Client, MSG501RECIPNO, MSG501RECIPNO_LEN);
                        break;
                    }

                    if ((ReplyInt =
                         RewriteAddress (name, To,
                                         sizeof (To))) == MAIL_BOGUS) {
                        SendClient (Client, MSG501RECIPNO, MSG501RECIPNO_LEN);
                    }
                    else {
                        switch (ReplyInt) {
                        case MAIL_REMOTE:{
                                if (!IsTrusted) {
                                    LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                                            LOGGER_EVENT_RECIPIENT_BLOCKED,
                                            LOG_INFO,
                                            "Recipient blocked %s %d",
                                            name,
                                            XplHostToLittle (Client->cs.
                                                             sin_addr.
                                                             s_addr));
                                    SendClient (Client, MSG571SPAMBLOCK,
                                                MSG571SPAMBLOCK_LEN);
                                    XplSafeIncrement (SMTPStats.SPAM.
                                                      DeniedRouting);
                                    goto QuitRcpt;
                                }
                                XplRWReadLockAcquire (&ConfigLock);
                                if (Client->RecipCount >= MaximumRecipients) {
                                    XplRWReadLockRelease (&ConfigLock);
                                    LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                                            LOGGER_EVENT_RECIPIENT_LIMIT_REACHED,
                                            LOG_INFO,
                                            "Recipient limit reached from %s to %s %d",
                                            Client->AuthFrom ?
                                            (char *) Client->AuthFrom : "",
                                            To,
                                            XplHostToLittle (Client->cs.
                                                             sin_addr.
                                                             s_addr));
                                    SendClient (Client, MSG550TOOMANY,
                                                MSG550TOOMANY_LEN);
                                    XplSafeIncrement (SMTPStats.SPAM.
                                                      DeniedRouting);
                                    goto QuitRcpt;
                                }
                                XplRWReadLockRelease (&ConfigLock);
                                snprintf (Answer, sizeof (Answer),
                                          "QSTOR TO %s %s %lu\r\n", To,
                                          Orcpt ? Orcpt : To,
                                          (unsigned long) (Client->
                                                           Flags &
                                                           DSN_FLAGS));
                                LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                                        LOGGER_EVENT_MESSAGE_RELAYED,
                                        LOG_INFO,
                                        "Message relayed from %s to %s %d",
                                        Client->AuthFrom ?
                                        (char *) Client->AuthFrom : "",
                                        To,
                                        XplHostToLittle (Client->cs.
                                                         sin_addr.s_addr));
                                XplSafeIncrement (SMTPStats.Recipient.
                                                  Received.Remote);
                                break;
                            }

                        case MAIL_RELAY:{
                                LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                                        LOGGER_EVENT_MESSAGE_RELAYED,
                                        LOG_INFO,
                                        "Message relayed from %s to %s %d",
                                        Client-> AuthFrom ?
                                        (char *) Client-> AuthFrom : "",
                                        To,
                                        XplHostToLittle (Client->cs.
                                                         sin_addr.
                                                         s_addr));
                                snprintf (Answer, sizeof (Answer),
                                          "QSTOR TO %s %s %lu\r\n", To,
                                          Orcpt ? Orcpt : To,
                                          (unsigned long) (Client->
                                                           Flags &
                                                           DSN_FLAGS));
                                XplSafeIncrement (SMTPStats.Recipient.
                                                  Received.Remote);
                                break;
                            }

                        case MAIL_LOCAL:{
                                XplRWReadLockAcquire (&ConfigLock);
                                if ((NullSender == FALSE)
                                    || (Client->RecipCount <
                                        MaxNullSenderRecips)) {
                                    if (!CheckRCPT) {
                                        XplRWReadLockRelease (&ConfigLock);
                                        snprintf (Answer, sizeof (Answer),
                                                  "QSTOR LOCAL %s %s %lu\r\n",
                                                  To, Orcpt ? Orcpt : To,
                                                  (unsigned long) (Client->
                                                                   Flags &
                                                                   DSN_FLAGS));
                                    }
                                    else {
                                        XplRWReadLockRelease (&ConfigLock);
                                        if (MsgFindObject
                                            (To, NULL, NULL, NULL, NULL)) {
                                            snprintf (Answer, sizeof (Answer),
                                                      "QSTOR LOCAL %s %s %lu\r\n",
                                                      To, Orcpt ? Orcpt : To,
                                                      (unsigned
                                                       long) (Client->
                                                              Flags &
                                                              DSN_FLAGS));
                                        }
                                        else {
                                            SendClient (Client,
                                                        MSG550NOTFOUND,
                                                        MSG550NOTFOUND_LEN);
                                            goto QuitRcpt;
                                        }
                                    }

                                    XplSafeIncrement (SMTPStats.Recipient.
                                                      Received.Local);
                                }
                                else {
                                    /* TODO - Inform the connection manager that this address should be blocked */

                                    XplRWReadLockRelease (&ConfigLock);

                                    TooManyNullSenderRecips = TRUE;
                                    XplDelay (250);

                                    SendClient (Client, MSG550TOOMANY,
                                                MSG550TOOMANY_LEN);
                                    XplSafeIncrement (SMTPStats.SPAM.
                                                      DeniedRouting);

                                    Client->RecipCount++;
                                    goto QuitRcpt;
                                }

                                break;
                            }
                        }
                        SendNMAPServer (Client, Answer, strlen (Answer));
                        if (GetNMAPAnswer
                            (Client, Reply, sizeof (Reply), TRUE) != 1000) {
                            SendClient (Client, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            if (Orcpt)
                                MemFree (Orcpt);
                            return (EndClientConnection (Client));
                        }
                        Client->State = STATE_TO;
                        Client->RecipCount++;
                        SendClient (Client, MSG250RECIPOK, MSG250RECIPOK_LEN);
                    }
                  QuitRcpt:
                    if (Orcpt)
                        MemFree (Orcpt);
                }
                break;

            default:
                SendClient (Client, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                break;
            }
            break;

        case 'M':{             /* MAIL FROM */
                unsigned long size = 0;
                unsigned char temp, *name;
                unsigned char *Envid = NULL;
                unsigned char *more;

                if (RequireAuth && !IsTrusted) {
                    SendClient (Client, MSG553SPAMBLOCK, MSG553SPAMBLOCK_LEN);
                    break;
                }

                if (Client->State >= STATE_FROM) {
                    SendClient (Client, MSG503BADORDER, MSG503BADORDER_LEN);
                    break;
                }

                if ((ptr = strchr (Client->Command, ':')) == NULL) {
                    SendClient (Client, MSG501SYNTAX, MSG501SYNTAX_LEN);
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
                    SendClient (Client, MSG501SYNTAX, MSG501SYNTAX_LEN);
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
                if (BlockRTSSpam && (name[0] == '\0')) {
                    XplRWReadLockAcquire (&ConfigLock);
                    time (&size);

                    if (LastBounce >= size - BounceInterval) {
                        /* Should make a decision */
                        LastBounce = size;
                        BounceCount++;
                        if (BounceCount > MaxBounceCount) {
                            /* We don't want the bounce, probably spam */
                            XplRWReadLockRelease (&ConfigLock);
                            XplSafeIncrement (SMTPStats.SPAM.Queue);
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
                            SendClient (Client, MSG501PARAMERROR,
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
                            SendClient (Client, MSG501PARAMERROR,
                                        MSG501PARAMERROR_LEN);
                            goto QuitMail;
                            break;
                        }
                        break;

                    case 'E':  /* ENVID */
                        ptr = strchr (more, '=');
                        if (!ptr) {
                            SendClient (Client, MSG501PARAMERROR,
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
                            SendClient (Client, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            goto QuitMail;
                        }
                        break;

                    case 'S':  /* SIZE */
                        ptr = strchr (more, '=');
                        if (!ptr) {
                            SendClient (Client, MSG501PARAMERROR,
                                        MSG501PARAMERROR_LEN);
                            goto QuitMail;
                            break;
                        }
                        ptr++;
                        size = atol (ptr);
                        XplRWReadLockAcquire (&ConfigLock);
                        if (MessageLimit > 0 && size > MessageLimit) {
                            XplRWReadLockRelease (&ConfigLock);
                            SendClient (Client, MSG552MSGTOOBIG,
                                        MSG552MSGTOOBIG_LEN);
                            goto QuitMail;
                        }
                        XplRWReadLockRelease (&ConfigLock);
                        break;

                    default:
                        SendClient (Client, MSG501PARAMERROR,
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
                    SendClient (Client, MSG501GOAWAY, MSG501GOAWAY_LEN);
                    if (Envid)
                        MemFree (Envid);
                    break;
                }

                ptr = strchr (name, '@');
                if (!ptr && ReplyInt > MAX_USERPART_LEN) {
                    if (Envid)
                        MemFree (Envid);
                    SendClient (Client, MSG501GOAWAY, MSG501GOAWAY_LEN);
                    break;
                }
                else if (ptr && ((ptr - name) > MAX_USERPART_LEN)) {
                    if (Envid)
                        MemFree (Envid);
                    SendClient (Client, MSG501GOAWAY, MSG501GOAWAY_LEN);
                    break;
                }


                if (size > 0) {
                    SendNMAPServer (Client, "QDSPC\r\n", 7);
                    if ((ReplyInt =
                         GetNMAPAnswer (Client, Reply, sizeof (Reply),
                                        TRUE)) != 1000) {
                        SendClient (Client, MSG451INTERNALERR,
                                    MSG451INTERNALERR_LEN);
                        if (Envid)
                            MemFree (Envid);
                        return (EndClientConnection (Client));
                    }
                    if ((unsigned long) atol (Reply) < size) {
                        SendClient (Client, MSG452NOSPACE, MSG452NOSPACE_LEN);
                        goto QuitMail;
                    }
                }
                SendNMAPServer (Client, "QCREA\r\n", 7);
                if ((ReplyInt =
                     GetNMAPAnswer (Client, Reply, sizeof (Reply),
                                    TRUE)) != 1000) {
                    if (ReplyInt == 5221) {
                        SendClient (Client, MSG452NOSPACE, MSG452NOSPACE_LEN);
                    }
                    else {
                        SendClient (Client, MSG451INTERNALERR,
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
                SendNMAPServer (Client, Answer, strlen (Answer));
                if (GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE) !=
                    1000) {
                    if (Envid) {
                        MemFree (Envid);
                    }

                    SendClient (Client, MSG451INTERNALERR,
                                MSG451INTERNALERR_LEN);
                    return (EndClientConnection (Client));
                }

                snprintf (Answer, sizeof (Answer), "QSTOR ADDRESS %u\r\n",
                          XplHostToLittle (Client->cs.sin_addr.s_addr));
                SendNMAPServer (Client, Answer, strlen (Answer));
                if (GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE) !=
                    1000) {
                    if (Envid) {
                        MemFree (Envid);
                    }

                    SendClient (Client, MSG451INTERNALERR,
                                MSG451INTERNALERR_LEN);
                    return (EndClientConnection (Client));
                }

                if (!(Client->Flags & DSN_HEADER)
                    && !(Client->Flags & DSN_BODY))
                    Client->Flags |= DSN_HEADER;
                Client->State = STATE_FROM;
                SendClient (Client, MSG250SENDEROK, MSG250SENDEROK_LEN);
              QuitMail:
                if (Envid) {
                    MemFree (Envid);
                }
            }
            break;

        case 'D':{             /* DATA */
                unsigned char TimeBuf[80];
                unsigned char Line[BUFSIZE + 1];
                long len;
                unsigned long BReceived = 0;
#ifdef USE_HOPCOUNT_DETECTION
                long HopCount = 0;
#endif

                if (Client->State < STATE_TO && Client->State != STATE_BDAT) {
                    SendClient (Client, MSG503BADORDER, MSG503BADORDER_LEN);
                    break;
                }

                if (Client->MsgFlags & MSG_FLAG_ENCODING_NONE) {
                    Client->MsgFlags &= ~MSG_FLAG_ENCODING_NONE;
                    Client->MsgFlags |= MSG_FLAG_ENCODING_7BIT;
                }

                Client->MsgFlags |= MSG_FLAG_SOURCE_EXTERNAL;
                snprintf (Answer, sizeof (Answer), "QSTOR FLAGS %d\r\n",
                          Client->MsgFlags);
                SendNMAPServer (Client, Answer, strlen (Answer));
                if (GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE) !=
                    1000) {
                    SendClient (Client, MSG451INTERNALERR,
                                MSG451INTERNALERR_LEN);
                    return (EndClientConnection (Client));
                }

                SendNMAPServer (Client, "QSTOR MESSAGE\r\n", 15);

                SendClient (Client, MSG354DATA, MSG354DATA_LEN);
                FlushClient (Client);

                MsgGetRFC822Date(-1, 0, TimeBuf);
                snprintf(Answer, sizeof(Answer),
                         "Received: from %d.%d.%d.%d (%s%s%s) [%d.%d.%d.%d]\r\n\tby %s with SMTP (%s %s%s);\r\n\t%s\r\n",
                         Client->cs.sin_addr.s_net,
                         Client->cs.sin_addr.s_host,
                         Client->cs.sin_addr.s_lh,
                         Client->cs.sin_addr.s_impno,
                         
                         Client->AuthFrom ? (char *)Client->AuthFrom : "not authenticated",
                         Client->RemoteHost[0] ? " HELO " : "",
                         Client->RemoteHost,
                         
                         Client->cs.sin_addr.s_net,
                         Client->cs.sin_addr.s_host,
                         Client->cs.sin_addr.s_lh,
                         Client->cs.sin_addr.s_impno,
                         
                         Hostname, 
                         PRODUCT_NAME,
                         PRODUCT_VERSION,
                         Client->ClientSSL ? "\r\n\tvia secured & encrypted transport [TLS]" : "",
                         TimeBuf);
                SendNMAPServer(Client, Answer, strlen(Answer));

/* 
	This is pretty ugly (and slow) - we have to detect a dot on a single line. The dot
	might already be in the input buffer; it might also be the only thing we get at all.
*/

                Ready = FALSE;
                while (!Ready) {
                    while ((Client->BufferPtr > 0)
                           &&
                           ((ptr =
                             strchrRN (Client->Buffer, 0x0a,
                                       Client->Buffer + Client->BufferPtr)) !=
                            NULL) && !Ready) {
                        len = ptr - Client->Buffer + 1;
                        memcpy (Line, Client->Buffer, len);
                        Client->BufferPtr -= len;
                        memmove (Client->Buffer, ptr + 1,
                                 Client->BufferPtr + 1);
                        Line[len] = 0;
                        if (Line[0] == '.' && Line[1] == 0x0d
                            && Line[2] == 0x0a) {
#if 0
                            if (!Licensed) {
                                SendNMAPServer (Client, "--\r\n", 4);
                                SendNMAPServer (Client,
                                                NMLicense.LicenseMessage,
                                                strlen (NMLicense.
                                                        LicenseMessage));
                                SendNMAPServer (Client, "\r\n", 2);
                            }
#endif
                            Ready = TRUE;
                            if (!SendNMAPServer (Client, ".\r\n", 3)) {
                                SendClient (Client, MSG451INTERNALERR,
                                            MSG451INTERNALERR_LEN);
                                return (EndClientConnection (Client));
                            }
                            if (GetNMAPAnswer
                                (Client, Reply, sizeof (Reply),
                                 TRUE) != 1000) {
                                SendClient (Client, MSG451INTERNALERR,
                                            MSG451INTERNALERR_LEN);
                                return (EndClientConnection (Client));
                            }
#ifdef USE_HOPCOUNT_DETECTION
                        }
                        else if (Line[0] == 'R' && Line[8] == ':') {
                            if (strncmp (Line, "Received:", 9) == 0) {
                                HopCount++;
                            }
                            if (!SendNMAPServer (Client, Line, len)) {
                                SendClient (Client, MSG451INTERNALERR,
                                            MSG451INTERNALERR_LEN);
                                return (EndClientConnection (Client));
                            }
#endif
                        }
                        else {
                            BReceived += len;
                            if (!SendNMAPServer (Client, Line, len)) {
                                SendClient (Client, MSG451INTERNALERR,
                                            MSG451INTERNALERR_LEN);
                                return (EndClientConnection (Client));
                            }
                        }
                    }
                    if (!Ready) {
                        if (Exiting) {
                            snprintf (Answer, sizeof (Answer),
                                      "421 %s %s\r\n", Hostname,
                                      MSG421SHUTDOWN);
                            SendClient (Client, Answer, strlen (Answer));
                            return (EndClientConnection (Client));
                        }
                        count =
                            DoClientRead (Client,
                                          Client->Buffer + Client->BufferPtr,
                                          LINESIZE - Client->BufferPtr, 0);
                        if (count < 1) {
                            if (Exiting) {
                                snprintf (Answer, sizeof (Answer),
                                          "421 %s %s\r\n", Hostname,
                                          MSG421SHUTDOWN);
                                SendClient (Client, Answer, strlen (Answer));
                            }
                            return (EndClientConnection (Client));
                        }
                        Client->BufferPtr += count;
                        Client->Buffer[Client->BufferPtr] = '\0';
                      RecheckDataLine:
                        if ((ptr =
                             strchrRN (Client->Buffer, 0x0a,
                                       Client->Buffer + Client->BufferPtr)) !=
                            NULL) {
                            len = ptr - Client->Buffer + 1;
                            memcpy (Line, Client->Buffer, len);
                            Line[len] = '\0';
                            Client->BufferPtr -= len;
                            memmove (Client->Buffer, ptr + 1,
                                     Client->BufferPtr);
                            Client->Buffer[Client->BufferPtr] = '\0';
                            if (Line[0] == '.' && Line[1] == 0x0d
                                && Line[2] == 0x0a) {
#if 0
                                if (!Licensed) {
                                    SendNMAPServer (Client, "--\r\n", 4);
                                    SendNMAPServer (Client,
                                                    NMLicense.LicenseMessage,
                                                    strlen (NMLicense.
                                                            LicenseMessage));
                                    SendNMAPServer (Client, "\r\n", 2);
                                }
#endif
                                Ready = TRUE;
                                if (!SendNMAPServer (Client, ".\r\n", 3)) {
                                    SendClient (Client, MSG451INTERNALERR,
                                                MSG451INTERNALERR_LEN);
                                    return (EndClientConnection (Client));
                                }
                                if (GetNMAPAnswer
                                    (Client, Reply, sizeof (Reply),
                                     TRUE) != 1000) {
                                    SendClient (Client, MSG451INTERNALERR,
                                                MSG451INTERNALERR_LEN);
                                    return (EndClientConnection (Client));
                                }
#ifdef USE_HOPCOUNT_DETECTION
                            }
                            else if (Line[0] == 'R' && Line[8] == ':') {
                                if (strncmp (Line, "Received:", 9) == 0) {
                                    HopCount++;
                                }
                                if (!SendNMAPServer (Client, Line, len)) {
                                    SendClient (Client, MSG451INTERNALERR,
                                                MSG451INTERNALERR_LEN);
                                    return (EndClientConnection (Client));
                                }
#endif
                            }
                            else {
                                BReceived += len;
                                if (!SendNMAPServer (Client, Line, len)) {
                                    SendClient (Client, MSG451INTERNALERR,
                                                MSG451INTERNALERR_LEN);
                                    return (EndClientConnection (Client));
                                }
                            }
                        }
                        else if (Client->BufferPtr >= LINESIZE) {
                            if (Client->BufferPtr >= BUFSIZE) {
                                SendClient (Client, MSG500TOOLONG,
                                            MSG500TOOLONG_LEN);
                                return (EndClientConnection (Client));
                            }
                            else {
                                Client->Buffer[Client->BufferPtr++] = 0x0d;
                                Client->Buffer[Client->BufferPtr++] = 0x0a;
                                Client->Buffer[Client->BufferPtr] = '\0';
                                goto RecheckDataLine;
                            }
                        }
                    }
                }

#ifdef USE_HOPCOUNT_DETECTION
                /* This is the best time to return to sender, we got the info and don't have to parse it again */
                if (HopCount > MAX_HOPS) {
                    fpos_t Pos;

                    fseek (Client->Control, 0, SEEK_SET);
                    while (!feof (Client->Control)) {
                        fgetpos (Client->Control, &Pos);
                        fgets (Answer, sizeof (Answer), Client->Control);
                        if (Answer[0] == 'F') {
                            fsetpos (Client->Control, &Pos);
                            fputc ('X', Client->Control);
                            break;
                        }
                    }
                }
#endif
                XplRWReadLockAcquire (&ConfigLock);
                if (MessageLimit > 0 && BReceived > MessageLimit) {     /* Message over limit */
                    XplRWReadLockRelease (&ConfigLock);
                    if (!SendNMAPServer (Client, "QABRT\r\n", 7) ||
                        GetNMAPAnswer (Client, Reply, sizeof (Reply),
                                       TRUE) != 1000) {
                        SendClient (Client, MSG451INTERNALERR,
                                    MSG451INTERNALERR_LEN);
                        return (EndClientConnection (Client));
                    }
                    SendClient (Client, MSG552MSGTOOBIG, MSG552MSGTOOBIG_LEN);
                }
                else {
                    XplRWReadLockRelease (&ConfigLock);

                    if ((NullSender == FALSE)
                        || (Client->RecipCount < MaxNullSenderRecips)) {
                        if (!SendNMAPServer (Client, "QRUN\r\n", 6)) {
                            SendClient (Client, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            return (EndClientConnection (Client));
                        }

                        LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                                LOGGER_EVENT_MESSAGE_RECEIVED,
                                LOG_INFO,
                                "Message received %s %d %d",
                                Client->From ? (char *) Client-> From : "",
                                XplHostToLittle (Client->cs.sin_addr.
                                                 s_addr),
                                BReceived);

                        if (Client->Flags & SENDER_LOCAL) {
                            XplSafeIncrement (SMTPStats.Message.Received.
                                              Local);
                            XplSafeAdd (SMTPStats.Byte.Received.Local,
                                        (BReceived + 1023) / 1024);
                        }
                        else {
                            XplSafeIncrement (SMTPStats.Message.Received.
                                              Remote);
                            XplSafeAdd (SMTPStats.Byte.Received.Remote,
                                        (BReceived + 1023) / 1024);
                        }

                        if (GetNMAPAnswer
                            (Client, Reply, sizeof (Reply), TRUE) != 1000) {
                            SendClient (Client, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            return (EndClientConnection (Client));
                        }

                        SendClient (Client, MSG250OK, MSG250OK_LEN);

                        NullSender = FALSE;
                        TooManyNullSenderRecips = FALSE;

                        Client->State = STATE_HELO;
                        Client->RecipCount = 0;

                        break;
                    }

                    LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                            LOGGER_EVENT_ADD_TO_BLOCK_LIST,
                            LOG_NOTICE,
                            "Added to block list %s %d / Count %d",
                            Client->From ? (char *) Client->From : "",
                            XplHostToLittle (Client->cs.sin_addr.s_addr),
                            Client->RecipCount);

                    /*      We are going to send him away!  */
                    SendClient (Client, MSG550TOOMANY, MSG550TOOMANY_LEN);

                    /*      EndClientConnection will send QABRT and QUIT to the NMAP server.        */
                    return (EndClientConnection (Client));
                }

                break;
            }
#if 0
            case 'B': {	  /* BDAT */
                unsigned char	TimeBuf[80];
                unsigned long	Size;
                unsigned long	Count	= 0;
                unsigned long	len;
                BOOL				Last	= FALSE;

                if (Client->State < STATE_TO) {
                    SendClient (Client, MSG503BADORDER, MSG503BADORDER_LEN);
                    break;
                }
                if (Client->Command[4] != ' ') {
                    SendClient (Client, MSG501SYNTAX, MSG501SYNTAX_LEN);
                    break;
                }

                Size = atol (Client->Command + 5);
                if (strchr (Client->Command + 5, ' ') != NULL)
                    Last = TRUE;


                /* We only want to write the Received line on the first BDAT */
                if (Client->State != STATE_BDAT) {
                    if (Client->MsgFlags & MSG_FLAG_ENCODING_NONE) {
                        Client->MsgFlags &= ~MSG_FLAG_ENCODING_NONE;
                        Client->MsgFlags |= MSG_FLAG_ENCODING_8BITM;
                        sprintf (Answer, "QSTOR FLAGS %lu\r\n",
                                 Client->MsgFlags);
                        SendNMAPServer (Client, Answer, strlen (Answer));
                        if (GetNMAPAnswer
                            (Client, Reply, sizeof (Reply), TRUE) != 1000) {
                            SendClient (Client, MSG451INTERNALERR,
                                        MSG451INTERNALERR_LEN);
                            return (EndClientConnection (Client));
                        }
                    }
                    SendNMAPServer (Client, "QSTOR MESSAGE\r\n", 15);

                    MsgGetRFC822Date(-1, 0, TimeBuf);
                    snprintf(Answer, sizeof(Answer),
                             "Received: from %d.%d.%d.%d (%s%s%s) [%d.%d.%d.%d]\r\n\tby %s with SMTP (%s %s%s);\r\n\t%s\r\n",
                             Client->cs.sin_addr.s_net,
                             Client->cs.sin_addr.s_host,
                             Client->cs.sin_addr.s_lh,
                             Client->cs.sin_addr.s_impno,
                             
                             Client->AuthFrom ? (char *)Client->AuthFrom : "not authenticated",
                             Client->RemoteHost[0] ? " HELO " : "",
                             Client->RemoteHost,
                             
                             Client->cs.sin_addr.s_net,
                             Client->cs.sin_addr.s_host,
                             Client->cs.sin_addr.s_lh,
                             Client->cs.sin_addr.s_impno,
                             
                             Hostname, 
                             PRODUCT_NAME,
                             PRODUCT_VERSION,
                             Client->ClientSSL ? "\r\n\tvia secured & encrypted transport [TLS]" : "",
                             TimeBuf);
                    
                    SendNMAPServer(Client, Answer, strlen(Answer));
                    
                    Client->State=STATE_BDAT;
                }
                
                while (Count < Size) {
                    len =
                        DoClientRead (Client, Client->Buffer,
                                      BUFSIZE <
                                      (Size - Count) ? BUFSIZE : Size - Count,
                                      0);
                    if (len < 1) {
                        if (Exiting) {
                            snprintf (Answer, sizeof (Answer),
                                      "421 %s %s\r\n", Hostname,
                                      MSG421SHUTDOWN);
                            SendClient (Client, Answer, strlen (Answer));
                        }
                        return (EndClientConnection (Client));
                    }
                    Count += len;
                    SendNMAPServer (Client, Client->Buffer, len);
                }

                if (Last) {
                    /* We do this because our maildrop parser relies on a blank line between messages */
                    SendNMAPServer (Client, "\r\n\r\n.\r\n", 7);
                    if (GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE) !=
                        1000) {
                        SendClient (Client, MSG451INTERNALERR,
                                    MSG451INTERNALERR_LEN);
                        return (EndClientConnection (Client));
                    }
                    Client->State = STATE_HELO;

                    NullSender = FALSE;
                    TooManyNullSenderRecips = FALSE;
                }

                SendClient (Client, MSG250OK, MSG250OK_LEN);
            }
            break;
#endif
        case 'Q':              /* QUIT */
            if (Client->State != STATE_DATA) {
                if (Client->State != STATE_FRESH) {
                    SendNMAPServer (Client, "QABRT\r\n", 7);
                    if (GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE) !=
                        1000) {
                        SendClient (Client, MSG451INTERNALERR,
                                    MSG451INTERNALERR_LEN);
                        return (EndClientConnection (Client));
                    }
                }
            }
            snprintf (Answer, sizeof (Answer), "221 %s %s\r\n", Hostname,
                      MSG221QUIT);
            SendClient (Client, Answer, strlen (Answer));
            return (EndClientConnection (Client));
            break;

        case 'N':              /* NOOP */
            SendClient (Client, MSG250OK, MSG250OK_LEN);
            break;

        case 'E':{
                switch (toupper (Client->Command[1])) {
                case 'H':{     /* EHLO */
                        if (Client->State == STATE_FRESH) {
                            XplRWReadLockAcquire (&ConfigLock);
                            if (MessageLimit > 0) {
                                snprintf (Answer, sizeof (Answer),
                                          "250-%s %s\r\n%s%s%s%s %lu\r\n",
                                          Hostname, MSG250HELLO,
                                          AcceptETRN ? MSG250ETRN : "",
                                          (AllowClientSSL
                                           && !Client->
                                           ClientSSL) ? MSG250TLS : "",
                                          (AllowAuth ==
                                           TRUE) ? MSG250AUTH : "",
                                          MSG250EHLO, MessageLimit);
                            }
                            else {
                                snprintf (Answer, sizeof (Answer),
                                          "250-%s %s\r\n%s%s%s%s\r\n",
                                          Hostname, MSG250HELLO,
                                          AcceptETRN ? MSG250ETRN : "",
                                          (AllowClientSSL
                                           && !Client->
                                           ClientSSL) ? MSG250TLS : "",
                                          (AllowAuth ==
                                           TRUE) ? MSG250AUTH : "",
                                          MSG250EHLO);
                            }
                            XplRWReadLockRelease (&ConfigLock);
                            SendClient (Client, Answer, strlen (Answer));
                            strncpy (Client->RemoteHost, Client->Command + 5,
                                     sizeof (Client->RemoteHost));
                            Client->State = STATE_HELO;

                            NullSender = FALSE;
                            TooManyNullSenderRecips = FALSE;
                        }
                        else {
                            SendClient (Client, MSG503BADORDER,
                                        MSG503BADORDER_LEN);
                        }
                    }
                    break;

                case 'T':{     /* ETRN */
                        count = 0;
                        XplRWReadLockAcquire (&ConfigLock);
                        if (AcceptETRN) {
                            XplRWReadLockRelease (&ConfigLock);
                            ReplyInt =
                                snprintf (Answer, sizeof (Answer),
                                          "QSRCH DOMAIN %s\r\n",
                                          Client->Command + 5);
                            SendNMAPServer (Client, Answer, ReplyInt);
                            while (GetNMAPAnswer
                                   (Client, Reply, sizeof (Reply),
                                    TRUE) == 2001) {
                                ReplyInt =
                                    snprintf (Answer, sizeof (Answer),
                                              "QRUN %s\r\n", Reply);
                                SendNMAPServer (Client, Answer, ReplyInt);
                                count++;
                            }
                            for (ReplyInt = 0; ReplyInt < count; ReplyInt++) {
                                GetNMAPAnswer (Client, Reply, sizeof (Reply),
                                               FALSE);
                            }
                            if (count > 0) {
                                SendClient (Client, MSG252QUEUESTARTED,
                                            MSG252QUEUESTARTED_LEN);
                            }
                            else {
                                SendClient (Client, MSG251NOMESSAGES,
                                            MSG251NOMESSAGES_LEN);
                            }
                        }
                        else {
                            XplRWReadLockRelease (&ConfigLock);
                            SendClient (Client, MSG502DISABLED,
                                        MSG502DISABLED_LEN);
                        }
                        break;
                    }

                case 'X':{     /* EXPN */
                        XplRWReadLockAcquire (&ConfigLock);
                        if (AllowEXPN) {
                            BOOL Found = FALSE;

                            XplRWReadLockRelease (&ConfigLock);
                            snprintf (Answer, sizeof (Answer), "VRFY %s\r\n",
                                      Client->Command + 5);
                            SendNMAPServer (Client, Answer, strlen (Answer));
                            while (GetNMAPAnswer
                                   (Client, Reply, sizeof (Reply),
                                    TRUE) != 1000) {
                                Found = TRUE;
                                SendClient (Client, "250-", 4);
                                SendClient (Client, Reply, strlen (Reply));
                                SendClient (Client, "\r\n", 2);
                            }

                            if (Found) {
                                SendClient (Client, MSG250OK, MSG250OK_LEN);
                            }
                            else {
                                SendClient (Client, MSG550NOTFOUND,
                                            MSG550NOTFOUND_LEN);
                            }
                        }
                        else {
                            XplRWReadLockRelease (&ConfigLock);
                            SendClient (Client, MSG502DISABLED,
                                        MSG502DISABLED_LEN);
                        }
                        break;
                    }
                default:
                    SendClient (Client, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
                    break;
                }
                break;
            }

        case 'V':{             /* VRFY */
                XplRWReadLockAcquire (&ConfigLock);
                if (AllowVRFY) {
                    BOOL Found = FALSE;

                    XplRWReadLockRelease (&ConfigLock);
                    snprintf (Answer, sizeof (Answer), "VRFY %s\r\n",
                              Client->Command + 5);
                    SendNMAPServer (Client, Answer, strlen (Answer));
                    while (GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE)
                           != 1000) {
                        Found = TRUE;
                        SendClient (Client, "250-", 4);
                        SendClient (Client, Reply, strlen (Reply));
                        SendClient (Client, "\r\n", 2);
                    }

                    if (Found) {
                        SendClient (Client, MSG250OK, MSG250OK_LEN);
                    }
                    else {
                        SendClient (Client, MSG550NOTFOUND,
                                    MSG550NOTFOUND_LEN);
                    }
                }
                else {
                    XplRWReadLockRelease (&ConfigLock);
                    SendClient (Client, MSG502DISABLED, MSG502DISABLED_LEN);
                }
                break;
            }

        default:
            SendClient (Client, MSG500UNKNOWN, MSG500UNKNOWN_LEN);
            break;
        }
        FlushClient (Client);
    }
    return (TRUE);
}

#define DELIVER_ERROR(_e)		{			\
	int i;										\
	for(i = 0; i < RecipCount; i++) {	\
		Recips[i].Result = _e;				\
	}												\
	if (Client->CSSL) {						\
		SSL_shutdown(Client->CSSL);		\
		SSL_free(Client->CSSL);				\
		Client->CSSL=NULL;					\
		Client->ClientSSL=FALSE;			\
	}												\
	return(_e);									\
}

#define DELIVER_ERROR_NO_RETURN(_e)	{	\
	int i;										\
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


static BOOL
FlushServer (ConnectionStruct * Client)
{
    int count;
    unsigned long sent = 0;

    while ((sent < Client->SBufferPtr) && (!Exiting)) {
        count =
            DoClientWrite (Client, Client->SBuffer + sent,
                           Client->SBufferPtr - sent, 0);
        if ((count < 1) || Exiting) {
            return (FALSE);
        }
        sent += count;
    }
    Client->SBufferPtr = 0;
    Client->SBuffer[0] = '\0';
    return (TRUE);
}

static BOOL
SendServer (ConnectionStruct * Client, unsigned char *Data, int Len)
{
    int sent = 0;

    if (!Len)
        return (TRUE);

    if (!Data)
        return (FALSE);

    while ((sent < Len) && !Exiting) {
        if (Len - sent + Client->SBufferPtr >= MTU) {
            memcpy (Client->SBuffer + Client->SBufferPtr, Data + sent,
                    (MTU - Client->SBufferPtr));
            sent += (MTU - Client->SBufferPtr);
            Client->SBufferPtr += (MTU - Client->SBufferPtr);
            if (!FlushServer (Client)) {
                return (FALSE);
            }
        }
        else {
            memcpy (Client->SBuffer + Client->SBufferPtr, Data + sent,
                    Len - sent);
            Client->SBufferPtr += Len - sent;
            sent = Len;
        }
    }
    Client->SBuffer[Client->SBufferPtr] = '\0';
    return (TRUE);
}

static int
GetEHLO (ConnectionStruct * Client, unsigned long *Extensions, long *Size)
{
    BOOL Ready;
    BOOL MultiLine;
    int count;
    int Result;
    unsigned char *ptr;
    unsigned char Reply[BUFSIZE];
    unsigned long lineLen;

    Reply[0] = '\0';
    *Size = 0;

    do {
        Ready = FALSE;
        if ((Client->BufferPtr > 0)
            &&
            ((ptr =
              strchrRN (Client->Buffer, 0x0a,
                        Client->Buffer + Client->BufferPtr)) != NULL)) {
            *ptr = '\0';
            lineLen = ptr - Client->Buffer + 1;
            memcpy (Reply, Client->Buffer, lineLen);
            Client->BufferPtr -= lineLen;
            memmove (Client->Buffer, ptr + 1, Client->BufferPtr + 1);
            if ((ptr = strrchr (Reply, 0x0d)) != NULL)
                *ptr = '\0';
            Ready = TRUE;
        }
        while (!Ready) {
            count =
                DoClientRead (Client, Client->Buffer + Client->BufferPtr,
                              BUFSIZE - Client->BufferPtr, 0);
            if ((count < 1) || (Exiting)) {
                return (-1);
            }
            Client->BufferPtr += count;
            Client->Buffer[Client->BufferPtr] = '\0';
            if ((ptr =
                 strchrRN (Client->Buffer, 0x0a,
                           Client->Buffer + Client->BufferPtr)) != NULL) {
                *ptr = '\0';
                lineLen = ptr - Client->Buffer + 1;
                memcpy (Reply, Client->Buffer, lineLen);
                Client->BufferPtr -= lineLen;
                memmove (Client->Buffer, ptr + 1, Client->BufferPtr + 1);
                if ((ptr = strrchr (Reply, 0x0d)) != NULL)
                    *ptr = '\0';
                Ready = TRUE;
            }
        }
        if (Reply[3] == '-') {
            MultiLine = TRUE;
            Reply[3] = ' ';
        }
        else {
            MultiLine = FALSE;
        }

        if (QuickCmp (Reply, "250 DSN"))
            *Extensions |= EXT_DSN;
        else if (QuickCmp (Reply, "250 PIPELINING"))
            *Extensions |= EXT_PIPELINING;
        else if (QuickCmp (Reply, "250 8BITMIME"))
            *Extensions |= EXT_8BITMIME;
        else if (QuickCmp (Reply, "250 AUTH=LOGIN"))
            *Extensions |= EXT_AUTH_LOGIN;
        else if (QuickCmp (Reply, "250 CHUNKING"))
            *Extensions |= EXT_CHUNKING;
        else if (QuickCmp (Reply, "250 BINARYMIME"))
            *Extensions |= EXT_BINARYMIME;
        else if (QuickCmp (Reply, "250 ETRN"))
            *Extensions |= EXT_ETRN;
        else if (QuickCmp (Reply, "250 STARTTLS"))
            *Extensions |= EXT_TLS;
        else if (QuickNCmp (Reply, "250 SIZE", 8)) {
            *Extensions |= EXT_SIZE;
            if (Reply[8] == ' ') {
                *Size = atol (Reply + 9);
            }
        }
    } while (MultiLine);

    ptr = strchr (Reply, ' ');
    if (!ptr) {
        return (-1);
    }
    *ptr = '\0';
    Result = atoi (Reply);
    return (Result);
}


static int
GetAnswer (ConnectionStruct * Client, unsigned char *Reply, int ReplyLen)
{
    BOOL Ready;
    BOOL MultiLine;
    int count;
    int Result;
    unsigned char *ptr;
    unsigned long lineLen;

    do {
        Ready = FALSE;
        if ((Client->BufferPtr > 0)
            &&
            ((ptr =
              strchrRN (Client->Buffer, 0x0a,
                        Client->Buffer + Client->BufferPtr)) != NULL)) {
            *ptr = '\0';
            lineLen = ptr - Client->Buffer + 1;
            memcpy (Reply, Client->Buffer, lineLen);
            Client->BufferPtr -= lineLen;
            memmove (Client->Buffer, ptr + 1, Client->BufferPtr + 1);
            if ((ptr = strrchr (Reply, 0x0d)) != NULL)
                *ptr = '\0';
            Ready = TRUE;
        }
        while (!Ready) {
            count =
                DoClientRead (Client, Client->Buffer + Client->BufferPtr,
                              BUFSIZE - Client->BufferPtr, 0);
            if ((count < 1) || (Exiting)) {
                memcpy (Reply, MSG422CONNERROR, MSG422CONNERROR_LEN + 1);
                return (422);
            }
            Client->BufferPtr += count;
            Client->Buffer[Client->BufferPtr] = '\0';
            if ((ptr =
                 strchrRN (Client->Buffer, 0x0a,
                           Client->Buffer + Client->BufferPtr)) != NULL) {
                *ptr = '\0';
                lineLen = ptr - Client->Buffer + 1;
                memcpy (Reply, Client->Buffer, lineLen);
                Client->BufferPtr -= lineLen;
                memmove (Client->Buffer, ptr + 1, Client->BufferPtr + 1);
                if ((ptr = strrchr (Reply, 0x0d)) != NULL)
                    *ptr = '\0';
                Ready = TRUE;
            }
        }
        if (Reply[3] == '-') {
            MultiLine = TRUE;
        }
        else {
            MultiLine = FALSE;
        }
    } while (MultiLine);
    ptr = strchr (Reply, ' ');
    if (!ptr)
        return (-1);
    *ptr = '\0';
    Result = atoi (Reply);
    memmove (Reply, ptr + 1, strlen (ptr + 1) + 1);
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
            if (!SendServer (Client, ".", 1)) {
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
                if (!SendServer (Client, ptr, Pos - (ptr - Data) + 1)) {
                    return (FALSE);
                }
                if (!SendServer (Client, ".", 1)) {
                    return (FALSE);
                }
                ptr = Data + Pos + 1;
            }
            Pos++;
        }
    }
    if (!SendServer (Client, ptr, Pos - (ptr - Data) + 1)) {
        return (FALSE);
    }
    if (Data[EndPos] == '\n') {
        *EscapedState = TRUE;
    }
    return (TRUE);
}

static int
DeliverSMTPMessage (ConnectionStruct * Client, unsigned char *Sender,
                    RecipStruct * Recips, int RecipCount,
                    unsigned int MsgFlags, unsigned char *Result,
                    int ResultLen)
{
    unsigned long Extensions = 0;
    unsigned char Answer[1026];
    unsigned char Reply[1024];
    unsigned char *ptr, *EnvID = NULL;
    int status;
    long Size, len, MessageSize;
    int i;
    BOOL EscapedState = FALSE;
    unsigned long LastNMAPContact;
        unsigned char	TimeBuf[80];

    status = GetAnswer (Client, Reply, sizeof (Reply));
    HandleFailure (220);

    snprintf (Answer, sizeof (Answer), "EHLO %s\r\n", Hostname);
    if (!SendServer (Client, Answer, strlen (Answer))
        || (!FlushServer (Client))) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }

    status = GetEHLO (Client, &Extensions, &Size);
    if (status != 250) {
        snprintf (Answer, sizeof (Answer), "HELO %s\r\n", Hostname);
        if (!SendServer (Client, Answer, strlen (Answer))
            || (!FlushServer (Client))) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
        status = GetAnswer (Client, Reply, sizeof (Reply));
        HandleFailure (250);
    }
    else {
        /* The other server supports ESMTP */

        /* Should we deliver via SSL? */
        if (AllowClientSSL && (Extensions & EXT_TLS)) {
            snprintf (Answer, sizeof (Answer), "STARTTLS\r\n");
            if (!SendServer (Client, Answer, strlen (Answer))
                || !FlushServer (Client)) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
            status = GetAnswer (Client, Reply, sizeof (Reply));
            if ((status / 100) == 2) {
                Client->ClientSSL = TRUE;
                Client->CSSL = SSL_new (SSLClientContext);
                if (Client->CSSL) {
                    if ((SSL_set_bsdfd (Client->CSSL, Client->s) == 1)
                        && (SSL_connect (Client->CSSL) == 1)) {
                        ;
                    }
                    else {
                        SSL_free (Client->CSSL);
                        Client->CSSL = NULL;
                        Client->ClientSSL = FALSE;
                    }
                }
                else {
                    Client->ClientSSL = FALSE;
                }
                status =
                    snprintf (Answer, sizeof (Answer), "EHLO %s\r\n",
                              Hostname);
                if (!SendServer (Client, Answer, status)
                    || !FlushServer (Client)) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
                status = GetAnswer (Client, Reply, sizeof (Reply));
            }
        }

        /* Do a size check */
        if ((Size > 0) && ((unsigned long) Size < Client->Flags)) {     /* Client->Flags holds size of data file */
            sprintf (Result,
                     "The recipient's server does not accept messages larger than %ld bytes. Your message was %ld bytes.",
                     Size, Client->Flags);
            sprintf (Answer, "QUIT\r\n");
            SendServer (Client, Answer, strlen (Answer));
            FlushServer (Client);
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
    if (!SendServer (Client, Answer, strlen (Answer))) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }

    if (Extensions & EXT_SIZE) {
        sprintf (Answer, " SIZE=%ld", Client->Flags);
        if (!SendServer (Client, Answer, strlen (Answer))) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
    }

    if (Extensions & EXT_DSN) {
        if (Recips[0].Flags & DSN_BODY) {
            if (!SendServer (Client, " RET=FULL", 9)) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
        }
        else if (Recips[0].Flags & DSN_HEADER) {
            if (!SendServer (Client, " RET=HDRS", 9)) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
        }
        if (EnvID) {
            snprintf (Answer, sizeof (Answer), " ENVID=%s", EnvID);
            if (!SendServer (Client, Answer, strlen (Answer))) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
        }
    }
    /* End of Reply variable protection area */

    if ((Extensions & EXT_8BITMIME) && (MsgFlags & MSG_FLAG_ENCODING_8BITM)) {
        if (!SendServer (Client, " BODY=8BITMIME", 14)) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
    }

    if (!SendServer (Client, "\r\n", 2)) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }

    if (!(Extensions & EXT_PIPELINING)) {
        if (!FlushServer (Client)) {
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
            SendNMAPServer (Client, "NOOP\r\n", 6);

            if (GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE) != 1000) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }
            else {
                LastNMAPContact = time (NULL);
            }
        }

        snprintf (Answer, sizeof (Answer), "RCPT TO:<%s>", Recips[i].To);
        if (!SendServer (Client, Answer, strlen (Answer))) {
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
                if (!SendServer (Client, Answer, strlen (Answer))) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
            }
            if (Recips[i].Flags & (DSN_SUCCESS | DSN_FAILURE | DSN_TIMEOUT)) {
                BOOL Comma = FALSE;
                if (!SendServer (Client, " NOTIFY=", 8)) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
                if (Recips[i].Flags & DSN_SUCCESS) {
                    if (!SendServer (Client, "SUCCESS", 7)) {
                        DELIVER_ERROR (DELIVER_TRY_LATER);
                    }
                    Comma = TRUE;
                }
                if (Recips[i].Flags & DSN_TIMEOUT) {
                    if (Comma) {
                        if (!SendServer (Client, ",DELAY", 6)) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                    else {
                        if (!SendServer (Client, "DELAY", 5)) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                    Comma = TRUE;
                }
                if (Recips[i].Flags & DSN_FAILURE) {
                    if (Comma) {
                        if (!SendServer (Client, ",FAILURE", 8)) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                    else {
                        if (!SendServer (Client, "FAILURE", 7)) {
                            DELIVER_ERROR (DELIVER_TRY_LATER);
                        }
                    }
                }
            }
            else {
                if (!SendServer (Client, " NOTIFY=NEVER", 13)) {
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }
            }
        }

        if (!SendServer (Client, "\r\n", 2)) {
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }


        if (!(Extensions & EXT_PIPELINING)) {
            if (!FlushServer (Client)) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }

            status = GetAnswer (Client, Reply, sizeof (Reply));
            if (Exiting) {
                DELIVER_ERROR (DELIVER_TRY_LATER);
            }

            if (status == 251 || status == 250) {
                LogMsg (LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_MESSAGE_SENT,
                        LOG_INFO,
                        "Message sent from %s to %s Flags %d Status %d",
                        Sender, Recips[i].To,
                        Client->Flags, status);
                Size++;
                continue;
            }
            else if (status / 100 == 4) {
                LogMsg (LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_MESSAGE_TRY_LATER,
                        LOG_INFO,
                        "Message try later from %s to %s Flags %d Status %d",
                        Sender, Recips[i].To,
                        Client->Flags, status);
                Recips[i].Result = DELIVER_TRY_LATER;
                continue;
            }
            else {
                LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                        LOGGER_EVENT_MESSAGE_FAILED_DELIVERY,
                        LOG_INFO,
                        "Message failed delivery from %s to %s Flags %d Status %d",
                        Sender, Recips[i].To,
                        Client->Flags, status);
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
        if (!FlushServer (Client)) {
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
                    LogMsg (LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_MESSAGE_SENT,
                            LOG_INFO,
                            "Message sent from %s to %s Flags %d Status %d",
                            Sender, Recips[i].To,
                            Client->Flags, status);
                    Size++;
                    continue;
                }
                else if (status / 100 == 4) {
                    LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                            LOGGER_EVENT_MESSAGE_TRY_LATER,
                            LOG_INFO,
                            "Message try later from %s to %s Flags %d Status %d",
                            Sender, Recips[i].To,
                            Client->Flags, status);
                    Recips[i].Result = DELIVER_TRY_LATER;
                    continue;
                }
                else {
                    LogMsg (LOGGER_SUBSYSTEM_QUEUE,
                            LOGGER_EVENT_MESSAGE_FAILED_DELIVERY,
                            LOG_INFO,
                            "Message failed delivery from %s to %s Flags %d Status %d",
                            Sender, Recips[i].To,
                            Client->Flags, status);
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
        if (Client->CSSL) {
            SSL_shutdown (Client->CSSL);
            SSL_free (Client->CSSL);
            Client->CSSL = NULL;
            Client->ClientSSL = FALSE;
        }
        return (DELIVER_FAILURE);
    }

    XplSafeAdd (SMTPStats.Recipient.Stored.Remote, i);

    if (!SendServer (Client, "DATA\r\n", 6) || (!FlushServer (Client))) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }
    status = GetAnswer (Client, Reply, sizeof (Reply));
    HandleFailure (354);

    MsgGetRFC822Date(-1, 0, TimeBuf);
    snprintf(Answer, sizeof(Answer), "Received: from %d.%d.%d.%d [%d.%d.%d.%d] by %s\r\n\twith NMAP (%s %s); %s\r\n",
             Client->cs.sin_addr.s_net,
             Client->cs.sin_addr.s_host,
             Client->cs.sin_addr.s_lh,
             Client->cs.sin_addr.s_impno,
             Client->cs.sin_addr.s_net,
             Client->cs.sin_addr.s_host,
             Client->cs.sin_addr.s_lh,
             Client->cs.sin_addr.s_impno,
             Hostname, 
             PRODUCT_NAME,
             PRODUCT_VERSION,
             TimeBuf);
    SendServer(Client, Answer, strlen(Answer));

    snprintf(Answer, sizeof(Answer), "QRETR %s MESSAGE\r\n", Client->RemoteHost);
    SendNMAPServer(Client, Answer, strlen(Answer));

    status = GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE);

    if (status != 2023) {
        DELIVER_ERROR (DELIVER_FAILURE);
    }
    Size = atol (Reply);
    if (!Size) {
        DELIVER_ERROR (DELIVER_FAILURE);
    }
        
    MessageSize = Size;

    /* Sending message to remote system; must escape any dots on a line */
    if (Client->NBufferPtr > 0) {
        if (Client->NBufferPtr > Size) {        /* Got the 1000 code in the buffer, too */
            if (!SendServerEscaped
                (Client, Client->NBuffer, Size, &EscapedState)) {
                DELIVER_ERROR_NO_RETURN (DELIVER_FAILURE);
            }
            memmove (Client->NBuffer, Client->NBuffer + Size,
                     Client->NBufferPtr - Size + 1);
            Client->NBufferPtr -= Size;
            Size = 0;
        }
        else {
            if (!SendServerEscaped
                (Client, Client->NBuffer, Client->NBufferPtr,
                 &EscapedState)) {
                DELIVER_ERROR_NO_RETURN (DELIVER_TRY_LATER);
            }
            Size -= Client->NBufferPtr;
            Client->NBufferPtr = 0;
            Client->NBuffer[0] = '\0';
        }
    }
    while (Size > 0) {
        if (Size < sizeof (Answer)) {
            len = DoNMAPRead (Client, Answer, Size, 0);
        }
        else {
            len = DoNMAPRead (Client, Answer, sizeof (Answer), 0);
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
    GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);       /* Get the 1000 OK message for QRETR MESG */

    if (!SendServer (Client, "\r\n.\r\n", 5) || (!FlushServer (Client))) {
        DELIVER_ERROR (DELIVER_TRY_LATER);
    }

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

    XplRWReadLockAcquire (&ConfigLock);
    if (SendETRN && (Extensions & EXT_ETRN)) {
        for (i = 0; i < DomainCount; i++) {
            len = snprintf (Reply, sizeof (Reply), "ETRN %s\r\n", Domains[i]);
            if (!SendServer (Client, Reply, len)) {
                XplRWReadLockRelease (&ConfigLock);
                if (Client->CSSL) {
                    SSL_shutdown (Client->CSSL);
                    SSL_free (Client->CSSL);
                    Client->CSSL = NULL;
                    Client->ClientSSL = FALSE;
                }
                return (DELIVER_SUCCESS);
            }
        }
    }
    XplRWReadLockRelease (&ConfigLock);

    XplSafeAdd (SMTPStats.Byte.Stored.Remote, (MessageSize + 1023) / 1024);
    XplSafeIncrement (SMTPStats.Message.Stored.Remote);

    if (SendServer (Client, "QUIT\r\n", 6) && (FlushServer (Client))) {
        status = GetAnswer (Client, Reply, sizeof (Reply));
    }

    if (!Client->CSSL) {
        return (DELIVER_SUCCESS);
    }
    else {
        SSL_shutdown (Client->CSSL);
        SSL_free (Client->CSSL);
        Client->CSSL = NULL;
        Client->ClientSSL = FALSE;
        return (DELIVER_SUCCESS);
    }

}

__inline static BOOL
IPAddressIsLocal (struct in_addr IPAddress)
{
    return (((IPAddress.s_addr) == LocalAddress) || ((IPAddress.s_net) == 127)
            || ((IPAddress.s_addr) == 0));
}

static int
DeliverRemoteMessage (ConnectionStruct * Client, unsigned char *Sender,
                      RecipStruct * Recips, int RecipCount,
                      unsigned long MsgFlags, unsigned char *Result,
                      int ResultLen)
{
    unsigned char Host[MAXEMAILNAMESIZE + 1];
    unsigned char *ptr;
    struct sockaddr_in soc_address;
    struct sockaddr_in *sin = &soc_address;
    int status;
    int RetVal;
    int IndexCnt, MXCnt;

    XplDnsRecord *DNSMXRec = NULL, *DNSARec = NULL;
    BOOL GoByAddr = FALSE, UsingMX = TRUE;

    ptr = strchr (Recips[0].To, '@');
    if (!ptr) {
        DELIVER_ERROR (DELIVER_BOGUS_NAME);
    }

    if (strlen (ptr + 1) < MAXEMAILNAMESIZE) {
        strcpy (Host, ptr + 1);
    }
    else {
        strncpy (Host, ptr + 1, MAXEMAILNAMESIZE);
        Host[MAXEMAILNAMESIZE] = '\0';
    }

    /* Clean up a potential ip address in the host name (e.g. jdoe@[1.1.1.1] ) */
    if (Host[0] == '[') {
        ptr = strchr (Host, ']');
        if (ptr)
            *ptr = '\0';
        memmove (Host, Host + 1, strlen (Host + 1) + 1);
        /* Check for an IP address */
        if ((sin->sin_addr.s_addr = inet_addr (Host)) != -1) {
            GoByAddr = TRUE;
            UsingMX = FALSE;
        }
    }

    if (!GoByAddr && UseRelayHost) {
        if ((sin->sin_addr.s_addr=inet_addr(RelayHost))==-1) {
            status=XplDnsResolve(RelayHost, &DNSARec, XPL_RR_A);
            /*fprintf (stderr, "%s:%d Looked up relay host %s: %d\n", __FILE__, __LINE__, RelayHost, status);*/
            switch(status) {
            case XPL_DNS_SUCCESS:
                break;
            case XPL_DNS_FAIL:
                strcpy (Result,
                        ">>> RelayHost Address Lookup\\r\\n<<< DNS Failure");
                DELIVER_ERROR (DELIVER_TRY_LATER);
                break;
            case XPL_DNS_TIMEOUT:
                strcpy (Result,
                        ">>> RelayHost Address Lookup\\r\\n<<< DNS Timeout");
                DELIVER_ERROR (DELIVER_TIMEOUT);
                break;

            case XPL_DNS_NORECORDS:
            case XPL_DNS_BADHOSTNAME:{
                    strcpy (Result,
                            ">>> RelayHost Address Lookup\\r\\n<<< DNS Host unknown");
                    DELIVER_ERROR (DELIVER_HOST_UNKNOWN);
                    break;
                }
            }
        }
        else {
            GoByAddr = TRUE;
        }
        UsingMX = FALSE;
    }
    
    if (!GoByAddr && UsingMX) {
        /*fprintf (stderr, "%s:%d using MX for %s\n", __FILE__, __LINE__, Host);*/
        MXCnt = 0;

        /* Get the MX for this host */
        status = XplDnsResolve (Host, &DNSMXRec, XPL_RR_MX);
        switch (status) {
        case XPL_DNS_SUCCESS:
            do {
                status =
                    XplDnsResolve (DNSMXRec[MXCnt].MX.name, &DNSARec,
                                   XPL_RR_A);
                MXCnt++;
            } while ((DNSMXRec[MXCnt].MX.name[0] != '\0')
                     && (status != XPL_DNS_SUCCESS));
            switch (status) {
            case XPL_DNS_SUCCESS:
                break;

            case XPL_DNS_FAIL:
                strcpy (Result, ">>> MX Address Lookup\\r\\n<<< DNS Failure");
                if (DNSMXRec) {
                    MemFree (DNSMXRec);
                }
                DELIVER_ERROR (DELIVER_TRY_LATER);
                break;

            case XPL_DNS_TIMEOUT:
                strcpy (Result, ">>> MX Address Lookup\\r\\n<<< DNS Timeout");
                if (DNSMXRec) {
                    MemFree (DNSMXRec);
                }
                DELIVER_ERROR (DELIVER_TIMEOUT);
                break;

            case XPL_DNS_NORECORDS:
            case XPL_DNS_BADHOSTNAME:
            default:
                strcpy (Result,
                        ">>> MX Address Lookup\\r\\n<<< DNS Host unknown");
                if (DNSMXRec) {
                    MemFree (DNSMXRec);
                }
                DELIVER_ERROR (DELIVER_HOST_UNKNOWN);
            }
            break;

        case XPL_DNS_FAIL:
            strcpy (Result, ">>> MX Lookup\\r\\n<<< DNS Failure");
            DELIVER_ERROR (DELIVER_TRY_LATER);
            break;

        case XPL_DNS_TIMEOUT:
            strcpy (Result, ">>> MX Lookup\\r\\n<<< DNS Timeout");
            DELIVER_ERROR (DELIVER_TIMEOUT);
            break;

        case XPL_DNS_NORECORDS:
            UsingMX = FALSE;
            status = XplDnsResolve (Host, &DNSARec, XPL_RR_A);
            switch (status) {
            case XPL_DNS_SUCCESS:
                break;

            case XPL_DNS_FAIL:{
                    strcpy (Result, ">>> MX Lookup\\r\\n<<< DNS Failure");
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                    break;
                }

            case XPL_DNS_TIMEOUT:{
                    strcpy (Result, ">>> MX Lookup\\r\\n<<< DNS Timeout");
                    DELIVER_ERROR (DELIVER_TIMEOUT);
                    break;
                }

            case XPL_DNS_BADHOSTNAME:
            case XPL_DNS_NORECORDS:{
                    strcpy (Result,
                            ">>> MX Lookup\\r\\n<<< DNS Host unknown");
                    DELIVER_ERROR (DELIVER_HOST_UNKNOWN);
                    break;
                }

            default:{
                    strcpy (Result,
                            ">>> MX Lookup\\r\\n<<< DNS Host unknown");
                    DELIVER_ERROR (DELIVER_HOST_UNKNOWN);
                    break;
                }
            }
            break;

        case XPL_DNS_BADHOSTNAME:
        default:{
                strcpy (Result, ">>> MX Lookup\\r\\n<<< DNS Host unknown");
                DELIVER_ERROR (DELIVER_HOST_UNKNOWN);
            }
        }
    }


    MXCnt = 0;

  LoopAgain:

    IndexCnt = 0;
    do {
        /* Need to reset Results in case we're on the second or whatever MX loop */
        for (status = 0; status < RecipCount; status++) {
            Recips[status].Result = 0;
        }

        if ((IndexCnt > 0) || (MXCnt > 0)) {
            /* Let NMAP know we're still here */
            SendNMAPServer (Client, "NOOP\r\n", 6);
            GetNMAPAnswer (Client, Result, ResultLen, TRUE);
            Result[0] = '\0';
        }

        if (!GoByAddr)
            memcpy (&sin->sin_addr, &DNSARec[IndexCnt].A.addr,
                    sizeof (struct in_addr));
        /* Create a connection */
        sin->sin_family = AF_INET;
        sin->sin_port = htons (25);
        if (Exiting) {
            if (DNSARec) {
                MemFree (DNSARec);
            }

            if (DNSMXRec) {
                MemFree (DNSMXRec);
            }
            
            DELIVER_ERROR (DELIVER_TRY_LATER);
        }
        if (IPAddressIsLocal(sin->sin_addr) && SMTPServerPort == 25) {
            BOOL ETRNMessage = FALSE;
            int i;
            // It's weird, but saves space...
            XplRWReadLockAcquire (&ConfigLock);
            for (i = 0; i < RelayDomainCount; i++) {
                if (QuickCmp ((Host), RelayDomains[i]) == TRUE) {
                    ETRNMessage = TRUE;
                    break;
                }
            }
            XplRWReadLockRelease (&ConfigLock);
            if (!ETRNMessage) {
                ptr = strchr (Recips[0].To, '@');
                if (ptr) {
                    if (sin->sin_addr.s_addr == LocalAddress) {
                        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                                LOGGER_EVENT_DNS_CONFIGURATION_ERROR,
                                LOG_ERROR,
                                "DNS config error %s %d",
                                ptr + 1,
                                XplHostToLittle (sin->sin_addr.s_addr));
                    }
                    else {
                        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                                LOGGER_EVENT_CONNECTION_LOCAL,
                                LOG_WARNING,
                                "Connection local %s %d",
                                ptr + 1,
                                XplHostToLittle (sin->sin_addr.s_addr));
                    }
                }
            
                if (DNSARec) {
                    MemFree (DNSARec);
                }

                if (DNSMXRec) {
                    MemFree (DNSMXRec);
                }

                DELIVER_ERROR (DELIVER_BOGUS_NAME);
            }
            else {
                RetVal = DELIVER_TRY_LATER;
            }
        }
        else {
            Client->s = IPsocket (PF_INET, SOCK_STREAM, IPPROTO_TCP);
            status =
                IPconnect (Client->s, (struct sockaddr *) &soc_address,
                           sizeof (soc_address));
        }
        if (status) {
            switch (errno) {
            case ETIMEDOUT:{
                    RetVal = DELIVER_TIMEOUT;
                    DELIVER_ERROR_NO_RETURN (RetVal);
                    strcpy (Result,
                            ">>> Connection attempt\\r\\n<<< Connection timeout");
                    break;
                }

            case ECONNREFUSED:{
                    RetVal = DELIVER_REFUSED;
                    strcpy (Result,
                            ">>> Connection attempt\\r\\n<<< Connection refused");
                    DELIVER_ERROR_NO_RETURN (RetVal);
                    break;
                }

            case ENETUNREACH:{
                    RetVal = DELIVER_UNREACHABLE;
                    strcpy (Result,
                            ">>> Connection attempt\\r\\n<<< Network unreachable");
                    DELIVER_ERROR_NO_RETURN (RetVal);
                    break;
                }

            default:{
                    RetVal = DELIVER_TRY_LATER;
                    strcpy (Result, "Could not connect to mail exchanger");
                    DELIVER_ERROR_NO_RETURN (RetVal);
                    break;
                }
            }
        }
        else {
            Result[0] = '\0';
            Client->BufferPtr = 0;
            Client->Buffer[0] = '\0';
            Client->SBufferPtr = 0;
            Client->SBuffer[0] = '\0';
            Client->NBufferPtr = 0;
            Client->NBuffer[0] = '\0';
            Client->ClientSSL = 0;
            Client->NMAPSSL = 0;

            XplSafeIncrement (SMTPStats.Server.Outgoing);
            RetVal =
                DeliverSMTPMessage (Client, Sender, Recips, RecipCount,
                                    MsgFlags, Result, ResultLen);
            XplSafeDecrement (SMTPStats.Server.Outgoing);
            XplSafeIncrement (SMTPStats.Server.ClosedOut);
        }
        IPclose (Client->s);
        Client->s = -1;

        if ((RetVal == DELIVER_SUCCESS) || GoByAddr || Exiting) {
            break;
        }

        IndexCnt++;
        if (DNSARec[IndexCnt].A.name[0] == '\0') {
            break;
        }
        else {
            if ((MaxMXServers != 0) && ((IndexCnt + 1) > MaxMXServers)
                && ((RetVal == DELIVER_FAILURE)
                    || (RetVal == DELIVER_USER_UNKNOWN))) {
                /* The administrator has indicated that no more than MaxMXServers       should be tried when 500 level errors are returned */
                break;
            }
        }
    } while (TRUE);

    /* This is the only decent way to avoid duplicating code for A & MX lookups */
    if (UsingMX && RetVal < DELIVER_SUCCESS && !Exiting) {
        MXCnt++;
        if (DNSARec) {
            MemFree (DNSARec);
            DNSARec = NULL;
        }

        if (DNSMXRec[MXCnt].MX.name[0] != '\0') {
            status =
                XplDnsResolve (DNSMXRec[MXCnt].MX.name, &DNSARec, XPL_RR_A);
            switch (status) {
            case XPL_DNS_SUCCESS:{
                    break;
                }

            case XPL_DNS_FAIL:{
                    if (DNSMXRec) {
                        MemFree (DNSMXRec);
                    }
                    strcpy (Result,
                            ">>> MX Address Lookup\\r\\n<<< DNS Failure");
                    DELIVER_ERROR (DELIVER_TRY_LATER);
                }

            case XPL_DNS_TIMEOUT:{
                    if (DNSMXRec) {
                        MemFree (DNSMXRec);
                    }
                    strcpy (Result,
                            ">>> MX Address Lookup\\r\\n<<< DNS Timeout");
                    DELIVER_ERROR (DELIVER_TIMEOUT);
                }

            case XPL_DNS_NORECORDS:
            case XPL_DNS_BADHOSTNAME:
            default:{
                    if (DNSMXRec) {
                        MemFree (DNSMXRec);
                    }
                    strcpy (Result,
                            ">>> MX Address Lookup\\r\\n<<< DNS Bad Hostname");
                    DELIVER_ERROR (DELIVER_HOST_UNKNOWN);
                }
            }
            goto LoopAgain;
        }
    }

    if (DNSARec)
        MemFree (DNSARec);

    if (DNSMXRec) {
        MemFree (DNSMXRec);
    }
//XplConsolePrintf("Returning %d for Recips[0]:%s, RecipCount:%d\n", RetVal, Recips[0].To, RecipCount);
    return (RetVal);
}

int
RewriteAddress (unsigned char *Source, unsigned char *Target, int TargetSize)
{
    unsigned char WorkSpace[1024];
    unsigned char *Src, *Dst;
    int i, RetVal = MAIL_BOGUS;

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
    RetVal = MAIL_REMOTE;
    /** Check if host matches local host **/
    Src = WorkSpace + strlen (WorkSpace) - 1;
    while (Src > WorkSpace) {
        if (*Src == '@') {
            XplRWReadLockAcquire (&ConfigLock);
            for (i = 0; i < UserDomainCount; i++) {
                if (XplStrCaseCmp ((Src + 1), UserDomains[i]) == 0) {
                    *Src = '\0';
                    if (strrchr (WorkSpace, '%') != NULL) {
                        RetVal = MAIL_BOGUS;
                    }
                    else {
                        RetVal = MAIL_LOCAL;
                    }
                    *Src = '@';
                    break;
                }
            }
            for (i = 0; i < RelayDomainCount; i++) {
                if (XplStrCaseCmp ((Src + 1), RelayDomains[i]) == 0) {
                    *Src = '\0';
                    if (strrchr (WorkSpace, '%') != NULL) {
                        RetVal = MAIL_REMOTE;
                    }
                    else {
                        RetVal = MAIL_RELAY;
                    }
                    *Src = '@';
                    break;
                }
            }
            if (RetVal == MAIL_REMOTE) {
                for (i = 0; i < DomainCount; i++) {
                    if (XplStrCaseCmp ((Src + 1), Domains[i]) == 0) {
                        *Src = '\0';
                        if ((Dst = strrchr (WorkSpace, '%')) != NULL) {
                            *Dst = '@';
                            RetVal = MAIL_REMOTE;
                        }
                        else {
                            RetVal = MAIL_LOCAL;
                        }
                        break;
                    }
                }
            }
            XplRWReadLockRelease (&ConfigLock);
        }
        Src--;
    }

    /* Clean address now in WorkSpace */

    if (WorkSpace[0] == '\0')
        return (MAIL_BOGUS);

    /** We're done **/
    if (strlen (WorkSpace) < TargetSize) {
        strcpy (Target, WorkSpace);
    }
    else {
        strncpy (Target, WorkSpace, TargetSize - 1);
        Target[TargetSize - 1] = '\0';
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
    unsigned char *Buffer;
    unsigned char *BufPtr;

    if (!Client)
        return;

    Lines += 3;
    Recips = MemMalloc (Size + (Lines * sizeof (RecipStruct)));
    if (!Recips) {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__,
                Size + (Lines * sizeof (RecipStruct)), __LINE__);
        return;
    }

    if (Client->From != NULL) {
        MemFree (Client->From);
    }
    Client->From = (unsigned char *) Recips;    /* This makes sure the recip structure gets freed if we */
    /* go into EndClientConnection for an error or shutdown */
    Buffer = (unsigned char *) (Recips + Lines);        /* This adds Lines * sizeof(RecipStruct) */
    BufPtr = Buffer;

    while ((rc =
            GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE)) != 6021) {
        switch (Reply[0]) {
        case QUEUE_FROM:{
                rc = snprintf (Result, sizeof (Result), "QMOD RAW %s\r\n",
                               Reply);
                SendNMAPServer (Client, Result, rc);
                strcpy (From, Reply + 1);
            }
            break;

        case QUEUE_FLAGS:{
                MsgFlags = atoi (Reply + 1);
            }
            break;

        case QUEUE_RECIP_REMOTE:{
                Recips[NumRecips].ORecip = NULL;
                Recips[NumRecips].Flags = DSN_FAILURE;
                Recips[NumRecips].Result = 0;

                ptr = strchr (Reply + 1, ' ');
                if (ptr) {
                    *ptr = '\0';
                }
                *BufPtr = '\0';

                rc = RewriteAddress (Reply + 1, BufPtr,
                                     Size - (BufPtr - Buffer));

                if (rc == MAIL_BOGUS) {
                    rc = DELIVER_BOGUS_NAME;
                    Recips[NumRecips].Result = rc;
                }
                Recips[NumRecips].To = BufPtr;
                BufPtr += strlen (BufPtr) + 1;  /* Leave NULL terminator */
                if (ptr) {
                    ptr2 = strchr (ptr + 1, ' ');
                    if (ptr2) {
                        *ptr2 = '\0';
                        Recips[NumRecips].Flags = atoi (ptr2 + 1);
                    }
                    strcpy (BufPtr, ptr + 1);
                    Recips[NumRecips].ORecip = BufPtr;
                    BufPtr += strlen (BufPtr) + 1;
                }
                if (Recips[NumRecips].Result == 0) {
                    //if(strchr(Recips[NumRecips].To, '@')) {}
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
                                SendNMAPServer (Client, Reply, rc);

                                SendNMAPServer (Client, Reply,
                                                snprintf (Reply,
                                                          sizeof (Reply),
                                                          "QSTOR FLAGS %ld\r\n",
                                                          MsgFlags));

                                rc = snprintf (Reply, sizeof (Reply),
                                               "QSTOR FROM %s\r\n", From);
                                SendNMAPServer (Client, Reply, rc);
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
                            SendNMAPServer (Client, Reply, rc);
                            BufPtr = Recips[NumRecips].To;      /* Reuse space */
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
                rc = snprintf (Result, sizeof (Result), "QMOD RAW %s\r\n",
                               Reply);
                SendNMAPServer (Client, Result, rc);
            }
            break;
        }
    }
    if (Local) {
        SendNMAPServer (Client, "QRUN\r\n", 6);
        GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);   /* QCOPY */
        GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);   /* QSTOR FLAGS */
        GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);   /* QSTOR FROM */
        for (i = 0; i < j; i++) {
            GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);       /* QSTOR LOCALS */
        }
        GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);   /* QRUN */
    }

    qsort (Recips, NumRecips, sizeof (RecipStruct), (void *) RecipCompare);

    i = 0;
    while (i < NumRecips) {
        rc = 1;
        while (i + rc < NumRecips
               && RecipCompare (&Recips[i], &Recips[i + rc]) == 0) {
            rc++;
        }
        Result[0] = '\0';

        DeliverRemoteMessage (Client, From, &Recips[i], rc, MsgFlags, Result,
                              BUFSIZE);

        for (j = i; j < (i + rc); j++) {
            switch (Recips[j].Result) {
            case DELIVER_SUCCESS:{
                    if (Recips[j].Flags & DSN_SUCCESS) {
                        len =
                            snprintf (Reply, sizeof (Reply),
                                      "QRTS %s %s %lu %d\r\n", Recips[j].To,
                                      Recips[j].ORecip, Recips[j].Flags,
                                      DELIVER_SUCCESS);
                        SendNMAPServer (Client, Reply, len);
                    }
                }
                break;

            case DELIVER_TIMEOUT:
            case DELIVER_REFUSED:
            case DELIVER_UNREACHABLE:
            case DELIVER_TRY_LATER:{
                    len =
                        snprintf (Reply, sizeof (Reply),
                                  "QMOD RAW R%s %s %lu\r\n", Recips[j].To,
                                  Recips[j].ORecip ? Recips[j].
                                  ORecip : Recips[j].To, Recips[j].Flags);
                    SendNMAPServer (Client, Reply, len);
                }
                break;

            case DELIVER_HOST_UNKNOWN:
            case DELIVER_USER_UNKNOWN:
            case DELIVER_BOGUS_NAME:
            case DELIVER_INTERNAL_ERROR:
            case DELIVER_FAILURE:{
                    if (Recips[j].Flags & DSN_FAILURE) {
                        if (Result[0] != '\0') {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QRTS %s %s %lu %d %s\r\n",
                                          Recips[j].To,
                                          Recips[j].ORecip ? Recips[j].
                                          ORecip : Recips[j].To,
                                          Recips[j].Flags, Recips[j].Result,
                                          Result);
                        }
                        else {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QRTS %s %s %lu %d\r\n",
                                          Recips[j].To,
                                          Recips[j].ORecip ? Recips[j].
                                          ORecip : Recips[j].To,
                                          Recips[j].Flags, Recips[j].Result);
                        }
                        SendNMAPServer (Client, Reply, len);
                    }
                }
                break;
            }
        }
        i += rc;
    }

    MemFree (Recips);           /* This includes Buffer */
    Client->From = NULL;
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
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__,
                size + (lines * sizeof (RecipStruct)), __LINE__);
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

    while ((rc =
            GetNMAPAnswer (client, reply, sizeof (reply), FALSE)) != 6021) {
        switch (reply[0]) {
        case QUEUE_FROM:{
                strcpy (from, reply + 1);
                SendNMAPServer (client, result,
                                snprintf (result, sizeof (result),
                                          "QMOD RAW %s\r\n", reply));
                continue;
            }

        case QUEUE_FLAGS:{
                msgFlags = atoi (reply + 1);
                SendNMAPServer (client, result,
                                snprintf (result, sizeof (result),
                                          "QMOD RAW %s\r\n", reply));
                continue;
            }

        case QUEUE_CALENDAR_LOCAL:{
                /* All calendar messages should be delivered locally. */
                SendNMAPServer (client, reply,
                                snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n",
                                          reply));
                continue;
            }

        case QUEUE_RECIP_LOCAL:
        case QUEUE_RECIP_MBOX_LOCAL:{
                if (msgFlags &
                    (MSG_FLAG_SOURCE_EXTERNAL | MSG_FLAG_CALENDAR_OBJECT)) {
                    /* Calendar messages should be delivered locally.
                       Externally received messages (relayed here) should be delivered locally. */
                    SendNMAPServer (client, reply,
                                    snprintf (reply, BUFSIZE,
                                              "QMOD RAW %s\r\n", reply));
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
                recips[recipCount].ORecip = NULL;
                recips[recipCount].Flags = DSN_FAILURE;
                recips[recipCount].Result = 0;

                /* R<recipient>[ <original recipient>[ flags]] */
                /* Look for the first optional ' ' delim between <recipient> and <original recipient> */
                ptr = strchr (reply + 1, ' ');
                if (ptr) {
                    *ptr = '\0';
                }

                *bufferPtr = '\0';

                rc = RewriteAddress (reply + 1, bufferPtr,
                                     size - (bufferPtr - buffer));
                if (rc == MAIL_BOGUS) {
                    rc = DELIVER_BOGUS_NAME;
                    recips[recipCount].Result = rc;
                }

                recips[recipCount].To = bufferPtr;

                /* Leave NULL terminator */
                bufferPtr += strlen (bufferPtr) + 1;
                if (ptr) {
                    ptr2 = strchr (ptr + 1, ' ');
                    if (ptr2) {
                        *ptr2 = '\0';
                        recips[recipCount].Flags = atoi (ptr2 + 1);
                    }

                    strcpy (bufferPtr, ptr + 1);
                    recips[recipCount].ORecip = bufferPtr;
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
                                    SendNMAPServer (client, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QCOPY %s\r\n",
                                                              client->
                                                              RemoteHost));
                                    SendNMAPServer (client, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QSTOR FLAGS %ld\r\n",
                                                              msgFlags));
                                    SendNMAPServer (client, reply,
                                                    snprintf (reply,
                                                              sizeof (reply),
                                                              "QSTOR FROM %s\r\n",
                                                              from));
                                    local = TRUE;
                                    j = 0;
                                }

                                if (recips[recipCount].ORecip) {
                                    SendNMAPServer (client, reply,
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
                                    SendNMAPServer (client, reply,
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
                                recips[recipCount].To =
                                    recips[recipCount].ORecip;
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
                SendNMAPServer (client, result,
                                snprintf (result, sizeof (result),
                                          "QMOD RAW %s\r\n", reply));
                break;
            }
        }
    }

    if (local) {
        SendNMAPServer (client, "QRUN\r\n", 6);

        /* QCOPY */
        GetNMAPAnswer (client, reply, sizeof (reply), FALSE);

        /* QSTOR FLAGS */
        GetNMAPAnswer (client, reply, sizeof (reply), FALSE);

        /* QSTOR FROM */
        GetNMAPAnswer (client, reply, sizeof (reply), FALSE);

        /* QSTOR LOCALS */
        for (i = 0; i < j; i++) {
            GetNMAPAnswer (client, reply, sizeof (reply), FALSE);
        }

        /* QRUN */
        GetNMAPAnswer (client, reply, sizeof (reply), FALSE);
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
                        SendNMAPServer (client, reply,
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
                    SendNMAPServer (client, reply,
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
                            SendNMAPServer (client, reply,
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
                            SendNMAPServer (client, reply,
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

static int
PullLine (unsigned char *Line, unsigned long LineSize,
          unsigned char **NextLine)
{
    unsigned char *ptr;

    ptr = *NextLine;
    *NextLine = strchr (ptr, '\n');
    if (*NextLine) {
        **NextLine = '\0';
        *NextLine = (*NextLine) + 1;
    }

    if (strlen (ptr) < LineSize) {
        strcpy (Line, ptr);
    }
    else {
        strncpy (Line, ptr, LineSize - 1);
        Line[LineSize - 1] = '\0';
    }

    if (*NextLine) {
        return (0);
    }
    else {
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
    while ((rc =
            GetNMAPAnswer (client, buffer, sizeof (buffer) - 1,
                           FALSE)) != 6021) {
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
                SendNMAPServer (client, reply,
                                snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n",
                                          buffer));
                continue;
            }

        case QUEUE_FLAGS:{
                msgFlags = atol (buffer + 1);
                SendNMAPServer (client, reply,
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

                rc = RewriteAddress (buffer + 1, to, sizeof (to));
                switch (rc) {
                case MAIL_BOGUS:{
                        if (flags & DSN_FAILURE) {
                            SendNMAPServer (client, reply,
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
                                SendNMAPServer (client, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QCOPY %s\r\n",
                                                          client->
                                                          RemoteHost));
                                GetNMAPAnswer (client, reply, BUFSIZE, FALSE);  /* QCOPY */

                                SendNMAPServer (client, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR FLAGS %lu\r\n",
                                                          msgFlags));
                                GetNMAPAnswer (client, reply, BUFSIZE, FALSE);  /* QSTOR FLAGS */

                                SendNMAPServer (client, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR FROM %s\r\n",
                                                          from));
                                GetNMAPAnswer (client, reply, BUFSIZE, FALSE);  /* QSTOR FROM */

                                Local = TRUE;
                            }

                            if (ptr) {
                                SendNMAPServer (client, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR LOCAL %s %s\r\n",
                                                          to, ptr + 1));
                            }
                            else {
                                SendNMAPServer (client, reply,
                                                snprintf (reply, BUFSIZE,
                                                          "QSTOR LOCAL %s\r\n",
                                                          to));
                            }

                            GetNMAPAnswer (client, reply, BUFSIZE, FALSE);      /* QSTOR LOCALS */
                        }
                        else if (!(msgFlags & MSG_FLAG_SOURCE_EXTERNAL)) {
                            if (ptr) {
                                *ptr = ' ';
                            }

                            /* All internally generated messages should be relayed. */
                            SendNMAPServer (client, reply,
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

                            SendNMAPServer (client, reply,
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
                    SendNMAPServer (client, reply,
                                    snprintf (reply, BUFSIZE,
                                              "QMOD RAW %s\r\n", buffer));
                }
                else if (!(msgFlags & MSG_FLAG_SOURCE_EXTERNAL)) {
                    /* All internally generated messages should be relayed. */
                    SendNMAPServer (client, reply,
                                    snprintf (reply, BUFSIZE,
                                              "QMOD RAW " QUEUES_RECIP_REMOTE
                                              "%s\r\n", buffer + 1));
                }

                continue;
            }

        default:{
                SendNMAPServer (client, reply,
                                snprintf (reply, BUFSIZE, "QMOD RAW %s\r\n",
                                          buffer));
                continue;
            }
        }
    }

    MemFree (envelope);

    if (Local) {
        SendNMAPServer (client, "QRUN\r\n", 6);
        GetNMAPAnswer (client, reply, BUFSIZE, FALSE);  /* QRUN */
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

    while ((rc = GetNMAPAnswer (Client, Line, sizeof (Line), FALSE)) != 6021) {
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
                SendNMAPServer (Client, Reply, len);
                strcpy (From, Line + 1);
            }
            break;

        case QUEUE_FLAGS:{
                msgFlags = atol (Line + 1);

                SendNMAPServer (Client, Reply,
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

                rc = RewriteAddress (Line + 1, To, sizeof (To));
                switch (rc) {
                case MAIL_BOGUS:{
                        if (Flags & DSN_FAILURE) {
                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QRTS %s %s %lu %d Addressformat not valid\r\n",
                                          To, ptr + 1,
                                          (long unsigned int) Flags,
                                          DELIVER_BOGUS_NAME);
                            SendNMAPServer (Client, Reply, len);
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
                        SendNMAPServer (Client, Reply, len);
                        break;
                    }

                default:{
                        if (!Local) {
                            len = snprintf (Reply, sizeof (Reply), "QCOPY %s\r\n", Client->RemoteHost); /* RemoteHost abused for queue id */
                            SendNMAPServer (Client, Reply, len);
                            GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);       /* QCOPY */

                            SendNMAPServer (Client, Reply,
                                            snprintf (Reply, sizeof (Reply),
                                                      "QSTOR FLAGS %ld\r\n",
                                                      msgFlags));
                            GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);       /* QSTOR FLAGS */

                            len =
                                snprintf (Reply, sizeof (Reply),
                                          "QSTOR FROM %s\r\n", From);
                            SendNMAPServer (Client, Reply, len);
                            GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);       /* QSTOR FROM */
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
                        SendNMAPServer (Client, Reply, len);
                        GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);   /* QSTOR LOCALS */
                        j++;
                        break;
                    }
                }
                break;
            }

        default:{
                len =
                    snprintf (Reply, sizeof (Reply), "QMOD RAW %s\r\n", Line);
                SendNMAPServer (Client, Reply, len);
                break;
            }
        }
    }

    MemFree (Envelope);

    if (Local) {
        SendNMAPServer (Client, "QRUN\r\n", 6);
        GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);   /* QRUN */
    }
}

static void
HandleQueueConnection (void *ClientIn)
{
    int ReplyInt, Queue;
    unsigned char Reply[1024];
    ConnectionStruct *Client = (ConnectionStruct *) ClientIn;
    unsigned char *ptr;
    unsigned long Lines;
    unsigned long Size;

    Client->State = STATE_WAITING;

    XplSafeIncrement (SMTPStats.IncomingQueueAgent);

    ReplyInt = GetNMAPAnswer (Client, Reply, sizeof (Reply), TRUE);

    if (ReplyInt != 6020) {
        SendNMAPServer (Client, "QDONE\r\n", 7);
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

    if (!RelayLocalMail) {
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

    SendNMAPServer (Client, "QDONE\r\n", 7);
    GetNMAPAnswer (Client, Reply, sizeof (Reply), FALSE);

    EndClientConnection (Client);
    return;
}

void
RegisterNMAPServer (void *QSIn)
{
    struct sockaddr_in soc_address;
    struct sockaddr_in *sin = &soc_address;
    ConnectionStruct *Client;
    unsigned char Answer[BUFSIZE + 1];
    unsigned char Name[MDB_MAX_OBJECT_CHARS + 1];
    int ReplyInt;
    QueueStartStruct *QS = (QueueStartStruct *) QSIn;
    int Queue, QSPort, i;

    Client = GetSMTPConnection ();
    if (!Client) {
        MemFree (QS);
        XplSafeDecrement (SMTPConnThreads);
        return;
    }

    Client->State = STATE_WAITING + 1;

    /* Connect to NMAP server */
    do {
        sin->sin_addr.s_addr = inet_addr (QS->serverAddress);
        sin->sin_family = AF_INET;
        sin->sin_port = htons (QS->serverPort);
        Client->NMAPs = IPsocket (PF_INET, SOCK_STREAM, IPPROTO_TCP);
        if (Client->NMAPs == -1) {
            ReturnSMTPConnection (Client);
            MemFree (QS);
            XplSafeDecrement (SMTPConnThreads);
            return;
        }
        ReplyInt =
            IPconnect (Client->NMAPs, (struct sockaddr *) &soc_address,
                       sizeof (soc_address));

        if (ReplyInt) {
            IPclose (Client->NMAPs);
            Client->NMAPs = -1;

            for (i = 0; (i < 15) && !Exiting; i++) {
                XplDelay (1000);
            }
        }
    } while (ReplyInt && !Exiting);

    LogMsg (LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_REGISTER_NMAP_SERVER,
            LOG_INFO, "Register nmap server: %s %d Queue %d",
                 (QS->SSL == TRUE) ? "TRUE" : "FALSE",
                 XplHostToLittle (soc_address.sin_addr.s_addr), QS->Queue);

    if (Exiting) {
        if (Client->NMAPs != -1) {
            IPclose (Client->NMAPs);
            Client->NMAPs = -1;
        }
        MemFree (QS);
        EndClientConnection (Client);
        return;
    }
    Queue = QS->Queue;
    QSPort = QS->agentPort;

    ReplyInt = GetNMAPAnswer (Client, Answer, sizeof (Answer), TRUE);

    switch (ReplyInt) {
    case NMAP_READY:{
            break;
        }

    case 4242:{
            unsigned char *ptr, *salt;
            MD5_CTX mdContext;
            unsigned char digest[16];
            unsigned char HexDigest[33];
            int i;

            ptr = strchr (Answer, '<');
            if (ptr) {
                ptr++;
                salt = ptr;
                if ((ptr = strchr (ptr, '>')) != NULL) {
                    *ptr = '\0';
                }

                MD5_Init(&mdContext);
                MD5_Update(&mdContext, salt, strlen(salt));
                MD5_Update(&mdContext, NMAPHash, NMAP_HASH_SIZE);
                MD5_Final(digest, &mdContext);
                for (i = 0; i < 16; i++) {
                    sprintf(HexDigest+(i*2),"%02x",digest[i]);
                }
                ReplyInt = sprintf(Answer, "AUTH SYSTEM %s\r\n", HexDigest);

                SendNMAPServer (Client, Answer, ReplyInt);
                if (GetNMAPAnswer (Client, Answer, sizeof (Answer), TRUE) ==
                    1000) {
                    break;
                }
            }
            /* Fall-through */
        }

    default:{
            IPclose (Client->NMAPs);
            Client->NMAPs = -1;
            MemFree (QS);
            EndClientConnection (Client);
            return;
        }
    }

    MemFree (QS);

    snprintf (Name, sizeof (Name), "%s%s%d", MsgGetServerDN (NULL),
              MSGSRV_AGENT_SMTP, Queue);
    snprintf (Answer, sizeof (Answer), "QWAIT %d %d %s\r\n", Queue,
              ntohs (QSPort), Name);
    if (SendNMAPServer (Client, Answer, strlen (Answer))) {
        ReplyInt = GetNMAPAnswer (Client, Answer, sizeof (Answer), TRUE);
    }
    else {
        ReplyInt = 5000;
    }
    IPclose (Client->NMAPs);
    Client->NMAPs = -1;

    if (ReplyInt != 1000) {
        EndClientConnection (Client);
        return;
    }

    ReturnSMTPConnection (Client);

    XplSafeDecrement (SMTPConnThreads);

    return;
}

__inline void
LaunchThreadToRegisterWithQueueServer(int agentPort, unsigned short serverPort, char *serverIp)
{
    QueueStartStruct *qs;
    int result;
    XplThreadID id;

    qs = MemMalloc (sizeof (QueueStartStruct));
    if (qs) {
        qs->Queue = Q_DELIVER;
        qs->SSL = FALSE;
        qs->agentPort = agentPort;
        qs->serverPort = serverPort;
        strcpy (qs->serverAddress, serverIp);

        XplBeginCountedThread (&id, RegisterNMAPServer, STACKSIZE_Q,
                               qs, result, SMTPConnThreads);
    }

    qs = MemMalloc (sizeof (QueueStartStruct));
    if (qs) {
        qs->Queue = Q_OUTGOING;
        qs->SSL = FALSE;
        qs->agentPort = agentPort;
        qs->serverPort = serverPort;
        strcpy (qs->serverAddress, serverIp);

        XplBeginCountedThread (&id, RegisterNMAPServer, STACKSIZE_Q,
                               qs, result, SMTPConnThreads);
    }
    return;
}

static void
RegisterNMAPQueueServers (int Port)
{
    unsigned short queueServerPort;
    char *ptr;
    char *queueServerAddress;
    int i;
    MDBValueStruct *queueServerDns;
    MDBValueStruct *details;

    queueServerDns = MDBCreateValueStruct (SMTPDirectoryHandle, MsgGetServerDN (NULL));
    if (!queueServerDns) {
        return;
    }

    MDBReadDN(MSGSRV_AGENT_SMTP, MSGSRV_A_MONITORED_QUEUE, queueServerDns);
    if ((queueServerDns->Used == 0) || (details = MDBCreateValueStruct(SMTPDirectoryHandle, NULL)) == NULL) {
        /* Failed to find a configuration object, use default connection information */
        MDBDestroyValueStruct(queueServerDns);
        LaunchThreadToRegisterWithQueueServer(Port, BONGO_QUEUE_PORT, "127.0.0.1");
        XplConsolePrintf("Couldn't find configuration object for %s, attempting to connect to NMAP on 127.0.0.1\n", MSGSRV_AGENT_SMTP);
        return;
    }

    for (i = 0; (i < queueServerDns->Used); i++) {
        /* check for a non-standard port */
        MDBRead(queueServerDns->Value[i], MSGSRV_A_PORT, details);
        if (details->Used == 0) {
            queueServerPort = BONGO_QUEUE_PORT;
        } else {
            queueServerPort = (unsigned short)atol(details->Value[0]);
            MDBFreeValues(details);
        }

        /* find the ip address on the host server object */
        if ((ptr = strrchr(queueServerDns->Value[i], '\\')) != NULL) {
            *ptr = '\0';
            MDBRead(queueServerDns->Value[i], MSGSRV_A_IP_ADDRESS, details);
            *ptr = '\\';
        } else {
            MDBRead(queueServerDns->Value[i], MSGSRV_A_IP_ADDRESS, details);
        }

        if (details->Used == 0) {
            queueServerAddress = "127.0.0.1";
        } else {
            queueServerAddress = details->Value[0];
        }

        LaunchThreadToRegisterWithQueueServer(Port, queueServerPort, queueServerAddress);
    }

    MDBDestroyValueStruct (details);
    MDBDestroyValueStruct (queueServerDns);

    return;
}

static void
QueueServerStartup (void *ignored)
{
    int ccode;
    int arg;
    int ds;
    struct sockaddr_in server_sockaddr;
    struct sockaddr_in client_sockaddr;
    ConnectionStruct *client;
    XplThreadID id;

    XplRenameThread (XplGetThreadID (), "SMTP NMAP Q Monitor");

    SMTPQServerSocket = IPsocket (PF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (SMTPQServerSocket == -1) {
        raise (SIGTERM);
        return;
    }

    ccode = 1;
    setsockopt (SMTPQServerSocket, SOL_SOCKET, SO_REUSEADDR,
                (unsigned char *) &ccode, sizeof (ccode));

    memset (&server_sockaddr, 0, sizeof (struct sockaddr));

    server_sockaddr.sin_family = AF_INET;
    server_sockaddr.sin_addr.s_addr = MsgGetAgentBindIPAddress ();

    ccode =
        IPbind (SMTPQServerSocket, (struct sockaddr *) &server_sockaddr,
                sizeof (server_sockaddr));
    if (ccode == -1) {
        IPclose (SMTPQServerSocket);
        SMTPQServerSocket = -1;

        raise (SIGTERM);
        return;
    }

    ccode = IPlisten (SMTPQServerSocket, 2048);
    if (ccode == -1) {
        IPclose (SMTPQServerSocket);
        SMTPQServerSocket = -1;

        raise (SIGTERM);
        return;
    }

    ccode = sizeof (server_sockaddr);
    IPgetsockname (SMTPQServerSocket, (struct sockaddr *) &server_sockaddr,
                   &ccode);
    SMTPQServerSocketPort = server_sockaddr.sin_port;

    RegisterNMAPQueueServers (SMTPQServerSocketPort);

    while (!Exiting) {
        arg = sizeof (client_sockaddr);
        ds = IPaccept (SMTPQServerSocket,
                       (struct sockaddr *) &client_sockaddr, &arg);
        if (!Exiting) {
            if (ds != -1) {
                client = GetSMTPConnection ();
                if (client) {
                    client->NMAPs = ds;
                    client->cs = client_sockaddr;

                    ccode = 1;
                    setsockopt (ds, IPPROTO_TCP, 1, (unsigned char *) &ccode,
                                sizeof (ccode));

                    XplBeginCountedThread (&id, HandleQueueConnection,
                                           STACKSIZE_Q, client, ccode,
                                           SMTPQueueThreads);
                    if (ccode == 0) {
                        continue;
                    }

                    ReturnSMTPConnection (client);
                }

                IPclose (ds);
                continue;
            }
            else {
                switch (errno) {
                case ECONNABORTED:
#ifdef EPROTO
                case EPROTO:
#endif
                case EINTR:{
                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ERROR,
                                "Accept failure in %s Errno %d",
                                "Queue Agent", errno);
                        break;
                    }

                default:{
                        XplConsolePrintf
                            ("SMTPD: Queueu Agent exiting after an accept() failure with an errno: %d\n",
                             errno);
                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ALERT,
                                "Accept failure %s Errno %d",
                                "Queue Agent", errno);

                        IPclose (SMTPQServerSocket);
                        SMTPQServerSocket = -1;
                        return;
                    }
                }

                continue;
            }
        }
        else {
            /*      Exiting set!    */
            IPclose (ds);

            break;
        }
    }

    XplSafeDecrement (SMTPServerThreads);

#if VERBOSE
    XplConsolePrintf ("SMTPD: Queue monitor thread done.\r\n");
#endif

    return;
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int
_NonAppCheckUnload (void)
{
    int s;
    static BOOL checked = FALSE;
    XplThreadID id;

    if (!checked) {
        checked = Exiting = TRUE;

        XplWaitOnLocalSemaphore (SMTPShutdownSemaphore);

        id = XplSetThreadGroupID (TGid);
        if (SMTPServerSocket != -1) {
            s = SMTPServerSocket;
            SMTPServerSocket = -1;

            IPclose (s);
        }
        XplSetThreadGroupID (id);

        XplWaitOnLocalSemaphore (SMTPServerSemaphore);
    }

    return (0);
}
#endif

static void
SMTPShutdownSigHandler (int sigtype)
{
    int s;
    static BOOL signaled = FALSE;

    if (!signaled && (sigtype == SIGTERM || sigtype == SIGINT)) {
        signaled = Exiting = TRUE;

        if (SMTPServerSocket != -1) {
            s = SMTPServerSocket;
            SMTPServerSocket = -1;

            IPshutdown (s, 2);
            IPclose (s);
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
    struct sockaddr_in server_sockaddr;

    SMTPServerSocket = IPsocket (PF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (SMTPServerSocket != -1) {
        ccode = 1;
        setsockopt (SMTPServerSocket, SOL_SOCKET, SO_REUSEADDR,
                    (unsigned char *) &ccode, sizeof (ccode));

        memset (&server_sockaddr, 0, sizeof (struct sockaddr));

        server_sockaddr.sin_family = AF_INET;
        server_sockaddr.sin_port = htons (SMTPServerPort);
        server_sockaddr.sin_addr.s_addr = MsgGetAgentBindIPAddress ();

        /* Get root privs back for the bind.  It's ok if this fails - 
         * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId (0);

        ccode =
            IPbind (SMTPServerSocket, (struct sockaddr *) &server_sockaddr,
                    sizeof (server_sockaddr));

        if (XplSetEffectiveUser (MsgGetUnprivilegedUser ()) < 0) {
            LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PRIV_FAILURE,
                    LOG_ERROR, "Priv failure User %s",
                         MsgGetUnprivilegedUser ());
            XplConsolePrintf
                ("bongosmtp: Could not drop to unprivileged user '%s'\n",
                 MsgGetUnprivilegedUser ());
            return -1;
        }

        if (ccode < 0) {
            LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_BIND_FAILURE,
                    LOG_ERROR, "Bind failure %s Port %d",
                    "SMTP ", SMTPServerPort);
            XplConsolePrintf ("bongosmtp: Could not bind to port %lu\n",
                              SMTPServerPort);
            return ccode;
        }
    }
    else {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_CREATE_SOCKET_FAILED,
                LOG_ERROR, "Create socket failed %s Line %d", "", __LINE__);
        XplConsolePrintf ("bongosmtp: Could not allocate socket.\n");
        return -1;
    }

    return 0;
}

static int
ServerSocketSSLInit (void)
{
    int ccode;
    struct sockaddr_in server_sockaddr;

    SMTPServerSocketSSL = IPsocket (PF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (SMTPServerSocketSSL != -1) {
        ccode = 1;
        setsockopt (SMTPServerSocketSSL, SOL_SOCKET, SO_REUSEADDR,
                    (unsigned char *) &ccode, sizeof (ccode));

        memset (&server_sockaddr, 0, sizeof (struct sockaddr));

        server_sockaddr.sin_family = AF_INET;
        server_sockaddr.sin_port = htons (SMTPServerPortSSL);
        server_sockaddr.sin_addr.s_addr = MsgGetAgentBindIPAddress ();

        /* Get root privs back for the bind.  It's ok if this fails - 
         * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId (0);

        ccode =
            IPbind (SMTPServerSocketSSL, (struct sockaddr *) &server_sockaddr,
                    sizeof (server_sockaddr));

        if (XplSetEffectiveUser (MsgGetUnprivilegedUser ()) < 0) {
            LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_PRIV_FAILURE,
                    LOG_ERROR, "Priv failure User %s",
                    MsgGetUnprivilegedUser ());
            XplConsolePrintf
                ("bongosmtp: Could not drop to unprivileged user '%s'\n",
                 MsgGetUnprivilegedUser ());
            return -1;
        }

        if (ccode < 0) {
            LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_BIND_FAILURE,
                    LOG_ERROR, "Bind failure %s Port %d",
                    "SMTP-SSL ", SMTPServerPortSSL);
            XplConsolePrintf ("bongosmtp: Could not bind to SSL port %lu\n",
                              SMTPServerPortSSL);
            return ccode;
        }
    }
    else {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_CREATE_SOCKET_FAILED,
                LOG_ERROR, "Create socket failed %s Line %d", "", __LINE__);
        XplConsolePrintf ("bongosmtp: Could not allocate SSL socket.\n");
        return -1;
    }

    return 0;
}

static void
SMTPServer (void *ignored)
{
    int ccode;
    int arg;
    int oldTGID;
    IPSOCKET ds;
    struct sockaddr_in client_sockaddr;
    ConnectionStruct *client;
    XplThreadID id;

    XplSafeIncrement (SMTPServerThreads);

    XplRenameThread (XplGetThreadID (), "SMTP Server");

    ccode = IPlisten (SMTPServerSocket, 2048);
    if (ccode != -1) {
        while (!Exiting) {
            arg = sizeof (client_sockaddr);
            ds = IPaccept (SMTPServerSocket,
                           (struct sockaddr *) &client_sockaddr, &arg);
            if (!Exiting) {
                if (ds != -1) {
                    if (!SMTPReceiverStopped) {
                        if (XplSafeRead (SMTPConnThreads) < SMTPMaxThreadLoad) {
                            client = GetSMTPConnection ();
                            if (client) {
                                client->s = ds;
                                client->cs = client_sockaddr;

                                ccode = 1;
                                setsockopt (ds, IPPROTO_TCP, 1,
                                            (unsigned char *) &ccode,
                                            sizeof (ccode));

                                XplBeginCountedThread (&id, HandleConnection,
                                                       STACKSIZE_S, client,
                                                       ccode,
                                                       SMTPConnThreads);
                                if (ccode == 0) {
                                    continue;
                                }

                                ReturnSMTPConnection (client);
                            }

                            IPsend (ds, MSG500NOMEMORY, MSG500NOMEMORY_LEN,
                                    0);
                        }
                        else {
                            IPsend (ds, MSG451NOCONNECTIONS,
                                    MSG451NOCONNECTIONS_LEN, 0);
                        }
                    }
                    else {
                        IPsend (ds, MSG451RECEIVERDOWN,
                                MSG451RECEIVERDOWN_LEN, 0);
                    }

                    IPclose (ds);
                    continue;
                }

                switch (errno) {
                case ECONNABORTED:
#ifdef EPROTO
                case EPROTO:
#endif
                case EINTR:{
                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ERROR, "Accept failure %s Errno %d",
                                "Server", errno);
                        continue;
                    }

                case EIO:{
                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ERROR, "Accept failure Errno %d %d",
                                errno, 2);
                        Exiting = TRUE;

                        break;
                    }

                default:{
                        arg = errno;

                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ALERT, "Accept failure %s Errno %d",
                                "Server", arg);
                        XplConsolePrintf
                            ("SMTPD: Exiting after an accept() failure with an errno: %d\n",
                             arg);

                        break;
                    }
                }

                break;
            }

            /*      Shutdown signaled!      */
            break;
        }
    }

/*	Shutting down!	*/
    Exiting = TRUE;

    oldTGID = XplSetThreadGroupID (TGid);

#if VERBOSE
    XplConsolePrintf ("\rSMTPD: Closing server sockets\r\n");
#endif
    if (SMTPServerSocket != -1) {
        IPshutdown (SMTPServerSocket, 2);
        IPclose (SMTPServerSocket);
        SMTPServerSocket = -1;
    }

    if (SMTPServerSocketSSL != -1) {
        IPshutdown (SMTPServerSocketSSL, 2);
        IPclose (SMTPServerSocketSSL);
        SMTPServerSocketSSL = -1;
    }

    if (SMTPQServerSocket != -1) {
        IPshutdown (SMTPQServerSocket, 2);
        IPclose (SMTPQServerSocket);
        SMTPQServerSocket = -1;
    }

    if (SMTPQServerSocket != -1) {
        IPshutdown (SMTPQServerSocketSSL, 2);
        IPclose (SMTPQServerSocketSSL);
        SMTPQServerSocket = -1;
    }

    /*      Management Client Shutdown      */
    if (ManagementState () == MANAGEMENT_RUNNING) {
        ManagementShutdown ();
    }

    for (arg = 0; (ManagementState () != MANAGEMENT_STOPPED) && (arg < 60);
         arg++) {
        XplDelay (1000);
    }

    /*      Wake up the children and set them free! */
    /* fixme - SocketShutdown; */

    /*      Wait for the our siblings to leave quietly.     */
    for (arg = 0; (XplSafeRead (SMTPServerThreads) > 1) && (arg < 60); arg++) {
        XplDelay (1000);
    }

    if (XplSafeRead (SMTPServerThreads) > 1) {
        XplConsolePrintf
            ("SMTPD: %d server threads outstanding; attempting forceful unload.\r\n",
             XplSafeRead (SMTPServerThreads) - 1);
    }

#if VERBOSE
    XplConsolePrintf ("SMTPD: Shutting down %d conn client threads and %d queue client threads\r\n",
                      XplSafeRead (SMTPConnThreads),
                      XplSafeRead (SMTPQueueThreads));
#endif

    /*      Make sure the kids have flown the coop. */
    for (arg = 0;
         (XplSafeRead (SMTPConnThreads) + XplSafeRead (SMTPQueueThreads))
         && (arg < 3 * 60); arg++) {
        XplDelay (1000);
    }

    if (XplSafeRead (SMTPConnThreads) + XplSafeRead (SMTPQueueThreads)) {
        XplConsolePrintf
            ("SMTPD: %d threads outstanding; attempting forceful unload.\r\n",
             XplSafeRead (SMTPConnThreads) + XplSafeRead (SMTPQueueThreads));
    }

#if VERBOSE
    XplConsolePrintf ("SMTPD: Freeing data structures\r\n");
#endif

    FreeDomains ();
    FreeUserDomains ();
    FreeRelayDomains ();

#if VERBOSE
    XplConsolePrintf ("SMTPD: Removing SSL data\r\n");
#endif

    /* Cleanup SSL */
    if (SSLContext) {
        SSL_CTX_free (SSLContext);
        SSLContext = NULL;
    }

    if (SSLClientContext) {
        SSL_CTX_free (SSLClientContext);
        SSLClientContext = NULL;
    }


    LogShutdown ();

    XplRWLockDestroy (&ConfigLock);
    MsgShutdown ();
//      MDBShutdown();

    ConnShutdown ();

    MemPrivatePoolFree (SMTPConnectionPool);
    MemoryManagerClose (MSGSRV_AGENT_SMTP);

#if VERBOSE
    XplConsolePrintf ("SMTPD: Shutdown complete.\r\n");
#endif

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
    int ccode;
    int arg;
    unsigned char *message;
    IPSOCKET ds;
    struct sockaddr_in server_sockaddr;
    struct sockaddr_in client_sockaddr;
    ConnectionStruct *client;
    SSL *cSSL = NULL;
    XplThreadID id = 0;

    XplRenameThread (XplGetThreadID (), "SMTP SSL Server");

    ccode = IPlisten (SMTPServerSocketSSL, 2048);
    if (ccode != -1) {
        while (!Exiting) {
            arg = sizeof (client_sockaddr);
            ds = IPaccept (SMTPServerSocketSSL,
                           (struct sockaddr *) &client_sockaddr, &arg);
            if (!Exiting) {
                if (ds != -1) {
                    if (!SMTPReceiverStopped) {
                        if (XplSafeRead (SMTPConnThreads) < SMTPMaxThreadLoad) {
                            client = GetSMTPConnection ();
                            if (client) {
                                client->s = ds;
                                client->cs = client_sockaddr;
                                client->ClientSSL = TRUE;
                                client->CSSL = SSL_new (SSLContext);
                                if (client->CSSL) {
                                    ccode =
                                        SSL_set_bsdfd (client->CSSL,
                                                       client->s);
                                    if (ccode == 1) {
                                        /* Set TCP non blocking io */
                                        setsockopt (ds, IPPROTO_TCP, 1,
                                                    (unsigned char *) &ccode,
                                                    sizeof (ccode));

                                        XplBeginCountedThread (&id,
                                                               HandleConnection,
                                                               STACKSIZE_S,
                                                               client, ccode,
                                                               SMTPConnThreads);
                                        if (ccode == 0) {
                                            continue;
                                        }
                                        if (SSL_accept (client->CSSL) == 1) {
                                            XplIPWriteSSL (client->CSSL,
                                                           "453 server error\r\n",
                                                           18);
                                        }
                                    }
                                    SSL_free (client->CSSL);
                                }

                                /* If we get here, we have already sent an error or SSL is not functional and we won't be able to */
                                ReturnSMTPConnection (client);
                                IPclose (ds);
                                continue;
                            }
                            else {
                                message = MSG500NOMEMORY;
                                arg = MSG500NOMEMORY_LEN;
                            }
                        }
                        else {
                            message = MSG451NOCONNECTIONS;
                            arg = MSG451NOCONNECTIONS_LEN;
                        }
                    }
                    else {
                        message = MSG451RECEIVERDOWN;
                        arg = MSG451RECEIVERDOWN_LEN;
                    }

                    cSSL = SSL_new (SSLContext);
                    if (cSSL) {
                        ccode = SSL_set_bsdfd (cSSL, ds);
                        if (ccode == 1) {
                            if (SSL_accept (cSSL) == 1) {
                                SSL_write (cSSL, message, arg);
                            }
                        }
                        SSL_free (cSSL);
                        cSSL = NULL;
                    }

                    IPclose (ds);
                    continue;
                }

                switch (errno) {
                case ECONNABORTED:
#ifdef EPROTO
                case EPROTO:
#endif
                case EINTR:{
                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ERROR, "Accept fialure %s Errno %d",
                                "SSL Server", errno);
                        continue;
                    }

                case EIO:{
                        XplDelay (3 * 1000);

                        IPclose (SMTPServerSocketSSL);
                        SMTPServerSocketSSL =
                            IPsocket (PF_INET, SOCK_STREAM, IPPROTO_TCP);
                        if (SMTPServerSocketSSL != -1) {
                            ccode = 1;
                            setsockopt (SMTPServerSocketSSL, SOL_SOCKET,
                                        SO_REUSEADDR,
                                        (unsigned char *) &ccode,
                                        sizeof (ccode));

                            memset (&server_sockaddr, 0,
                                    sizeof (struct sockaddr));
                            server_sockaddr.sin_family = AF_INET;
                            server_sockaddr.sin_port =
                                htons (SMTPServerPortSSL);
                            server_sockaddr.sin_addr.s_addr =
                                MsgGetAgentBindIPAddress ();

                            ccode =
                                IPbind (SMTPServerSocketSSL,
                                        (struct sockaddr *) &server_sockaddr,
                                        sizeof (server_sockaddr));
                            if (ccode != -1) {
                                ccode = IPlisten (SMTPServerSocketSSL, 2048);
                                if (ccode != -1) {
                                    LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                            LOGGER_EVENT_ACCEPT_FAILURE,
                                            LOG_ERROR,
                                            "Accept failure %s Errno %d %d",
                                            "SSL Server", errno, 2);
                                    continue;
                                }
                            }
                        }

                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ERROR, "Accept failure %s Errno %d %d",
                                "SSL Server", errno, 3);

                        break;
                    }

                default:{
                        XplConsolePrintf
                            ("SMTPD: SSL Agent exiting after an accept() failure with an errno: %d\n",
                             errno);
                        LogMsg (LOGGER_SUBSYSTEM_GENERAL,
                                LOGGER_EVENT_ACCEPT_FAILURE,
                                LOG_ALERT, "Accept failure %s Errno %d",
                                "SSL Server", errno);

                        break;
                    }
                }

                break;
            }

            /*      Shutdown signaled!      */
            break;
        }
    }

    XplSafeDecrement (SMTPServerThreads);

#if VERBOSE
    XplConsolePrintf ("SMTPD: SSL listening thread done.\r\n");
#endif

    if (!Exiting) {
        raise (SIGTERM);
    }

    return;
}

static void
AddDomain (unsigned char *DomainValue)
{
    Domains =
        MemRealloc (Domains, (DomainCount + 1) * sizeof (unsigned char *));
    if (!Domains) {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__,
                (DomainCount + 1) * sizeof (unsigned char *), __LINE__);
        return;
    }
    Domains[DomainCount] = MemStrdup (DomainValue);
    if (!Domains[DomainCount]) {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__, strlen (DomainValue) + 1, __LINE__);
        return;
    }
    DomainCount++;
}

void
FreeDomains (void)
{
    int i;

    for (i = 0; i < DomainCount; i++)
        MemFree (Domains[i]);

    if (Domains) {
        MemFree (Domains);
    }

    Domains = NULL;
    DomainCount = 0;
}

static void
AddUserDomain (unsigned char *UserDomainValue)
{
    UserDomains =
        MemRealloc (UserDomains,
                    (UserDomainCount + 1) * sizeof (unsigned char *));
    if (!UserDomains) {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__,
                (UserDomainCount + 1) * sizeof (unsigned char *), __LINE__);
        return;
    }
    UserDomains[UserDomainCount] = MemStrdup (UserDomainValue);
    if (!UserDomains[UserDomainCount]) {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__, strlen (UserDomainValue) + 1, __LINE__);
        return;
    }
    UserDomainCount++;
}

void
FreeUserDomains (void)
{
    int i;

    for (i = 0; i < UserDomainCount; i++)
        MemFree (UserDomains[i]);

    if (UserDomains) {
        MemFree (UserDomains);
    }

    UserDomains = NULL;
    UserDomainCount = 0;
}

static void
AddRelayDomain (unsigned char *RelayDomainValue)
{
    RelayDomains =
        MemRealloc (RelayDomains,
                    (RelayDomainCount + 1) * sizeof (unsigned char *));
    if (!RelayDomains) {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__,
                (RelayDomainCount + 1) * sizeof (unsigned char *), __LINE__);
        return;
    }
    RelayDomains[RelayDomainCount] = MemStrdup (RelayDomainValue);
    if (!RelayDomains[RelayDomainCount]) {
        LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY,
                LOG_ERROR, "Out of memory File %s %d Line %d",
                __FILE__, strlen (RelayDomainValue) + 1, __LINE__);
        return;
    }
    RelayDomainCount++;
}

void
FreeRelayDomains (void)
{
    int i;

    for (i = 0; i < RelayDomainCount; i++)
        MemFree (RelayDomains[i]);

    if (RelayDomains) {
        MemFree (RelayDomains);
    }

    RelayDomains = NULL;
    RelayDomainCount = 0;
}

#define	SetPtrToValue(Ptr,String)	Ptr=String;while(isspace(*Ptr)) Ptr++;if ((*Ptr=='=') || (*Ptr==':')) Ptr++; while(isspace(*Ptr)) Ptr++;

static void
SmtpdConfigMonitor (void)
{
    MDBValueStruct *Config;
    MDBValueStruct *Parents;
    int i, j;
    struct sockaddr_in soc_address;
    struct hostent *he;
    struct sockaddr_in *sin = &soc_address;
    BOOL Added, Exists;
    char *ptr;
    MDBEnumStruct *ES;
    unsigned long PrevConfigNumber;

    XplRenameThread (XplGetThreadID (), "SMTP Config Monitor");

    Config =
        MDBCreateValueStruct (SMTPDirectoryHandle, MsgGetServerDN (NULL));
    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_CONFIG_CHANGED, Config) > 0) {
        PrevConfigNumber = atol (Config->Value[0]);
    }
    else {
        PrevConfigNumber = 0;
    }
    MDBDestroyValueStruct (Config);

    while (!Exiting) {
        Config =
            MDBCreateValueStruct (SMTPDirectoryHandle, MsgGetServerDN (NULL));
        Parents =
            MDBCreateValueStruct (SMTPDirectoryHandle, MsgGetServerDN (NULL));

        ES = MDBCreateEnumStruct (Config);

        for (i = 0; (i < 300) && !Exiting; i++) {
            XplDelay (1000);
        }

        if (!Exiting) {
            LogMsg (LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_AGENT_HEARTBEAT,
                    LOG_INFO, "Agent heartbeat");

            if ((MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_CONFIG_CHANGED, Config)
                 > 0) && (atol (Config->Value[0]) != PrevConfigNumber)) {
                /* Clear what we just read */
                PrevConfigNumber = atol (Config->Value[0]);
                MDBFreeValues (Config);

                /* Acquire Write Lock */
                XplRWWriteLockAcquire (&ConfigLock);

                FreeDomains ();
                FreeUserDomains ();
                FreeRelayDomains ();

                soc_address.sin_addr.s_addr = XplGetHostIPAddress ();
                sprintf (Hostaddr, "[%d.%d.%d.%d]", sin->sin_addr.s_net,
                         sin->sin_addr.s_host, sin->sin_addr.s_lh,
                         sin->sin_addr.s_impno);
                AddDomain (Hostaddr);

                strcpy (OfficialName, Hostname);

                MDBReadDN (MSGSRV_AGENT_SMTP, MSGSRV_A_PARENT_OBJECT,
                           Parents);

                if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_DOMAIN, Config) > 0) {
                    for (i = 0; i < Config->Used; i++) {
                        AddDomain (Config->Value[i]);
                        /*      XplConsolePrintf("[%04d] MDB_A_DOMAIN:%s", __LINE__, Config->Value[i]); */
                    }
                }
                he = gethostbyaddr ((unsigned char *)
                                    &(soc_address.sin_addr.s_addr),
                                    sizeof (soc_address.sin_addr.s_addr),
                                    AF_INET);

                Added = FALSE;
                if (he) {
                    Exists = FALSE;
                    for (j = 0; j < DomainCount; j++) {
                        if (XplStrCaseCmp (Domains[j], he->h_name) == 0) {
                            Exists = TRUE;
                            break;
                        }
                    }
                    if (!Exists) {
                        AddDomain (he->h_name);
                        MDBAddValue (he->h_name, Config);
                        Added = TRUE;
                    }

                    if (he->h_aliases) {
                        i = 0;
                        while (he->h_aliases[i]) {
                            Exists = FALSE;
                            for (j = 0; j < DomainCount; j++) {
                                if (XplStrCaseCmp
                                    (Domains[j], he->h_aliases[i]) == 0) {
                                    Exists = TRUE;
                                    break;
                                }
                            }
                            if (!Exists) {
                                AddDomain (he->h_aliases[i]);
                                MDBAddValue (he->h_aliases[i], Config);
                                Added = TRUE;
                            }
                            i++;
                        }
                    }
                }

                if (Added) {
                    MDBWrite (MSGSRV_AGENT_SMTP, MSGSRV_A_DOMAIN, Config);
                }

                MDBFreeValues (Config);

                for (j = 0; j < Parents->Used; j++) {
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_STRING,
                            LOG_INFO, "Configuration string %s Value %s"
                            "MSGSRV_A_PARENT_OBJECT", Parents->Value[j]);
                    if (MDBRead (Parents->Value[j], MSGSRV_A_DOMAIN, Config)) {
                        for (i = 0; i < Config->Used; i++) {
                            AddDomain (Config->Value[i]);
                            LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                                    LOGGER_EVENT_CONFIGURATION_STRING,
                                    LOG_INFO,
                                    "Configuration string %s Value %s",
                                    "MSGSRV_A_DOMAIN", Config->Value[i]);
                        }
                    }
                    MDBFreeValues (Config);
                }

                if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_USER_DOMAIN, Config)
                    > 0) {
                    for (i = 0; i < Config->Used; i++) {
                        AddUserDomain (Config->Value[i]);
                        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                                LOGGER_EVENT_CONFIGURATION_STRING,
                                LOG_INFO, "Configuration string %s Value %s",
                                "MSGSRV_A_USER_DOMAIN", Config->Value[i]);
                    }
                }
                MDBFreeValues (Config);

                for (j = 0; j < Parents->Used; j++) {
                    if (MDBRead
                        (Parents->Value[j], MSGSRV_A_USER_DOMAIN, Config)) {
                        for (i = 0; i < Config->Used; i++) {
                            AddUserDomain (Config->Value[i]);
                            LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                                    LOGGER_EVENT_CONFIGURATION_STRING,
                                    LOG_INFO,
                                    "Configuration string %s Value %s",
                                    "MSGSRV_A_USER_DOMAIN", Config->Value[i]);
                        }
                    }
                    MDBFreeValues (Config);
                }

                if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_RELAY_DOMAIN, Config)
                    > 0) {
                    for (i = 0; i < Config->Used; i++) {
                        AddRelayDomain (Config->Value[i]);
                        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                                LOGGER_EVENT_CONFIGURATION_STRING,
                                LOG_INFO, "Configuration string %s Value %s",
                                "MSGSRV_A_RELAY_DOMAIN", Config->Value[i]);
                    }
                }
                for (j = 0; j < Parents->Used; j++) {
                    if (MDBRead
                        (Parents->Value[j], MSGSRV_A_RELAY_DOMAIN, Config)) {
                        for (i = 0; i < Config->Used; i++) {
                            AddRelayDomain (Config->Value[i]);
                            LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                                    LOGGER_EVENT_CONFIGURATION_STRING,
                                    LOG_INFO,
                                    "Configuration string %s Value %s",
                                    "MSGSRV_A_RELAY_DOMAIN", Config->Value[i]);
                        }
                    }
                    MDBFreeValues (Config);
                }

                if (MDBRead (".", MSGSRV_A_OFFICIAL_NAME, Config) > 0) {
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_STRING,
                            LOG_INFO, "Configuration string %s Value %s",
                            "MSGSRV_A_OFFICIAL_NAME", Config->Value[0]);
                    AddDomain (Hostname);
                }
                MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_ALLOW_EXPN, Config)) {
                    AllowEXPN = atol (Config->Value[0]);
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Configuration numeric %s Value %d",
                            "MSGSRV_A_SMTP_ALLOW_EXPN", AllowEXPN);
                }
                MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_ALLOW_VRFY, Config)) {
                    AllowVRFY = atol (Config->Value[0]);
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Configuration numeric %s Value %d",
                            "MSGSRV_A_SMTP_ALLOW_VRFY", AllowVRFY);
                }
                MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_VERIFY_ADDRESS,
                     Config)) {
                    CheckRCPT = atol (Config->Value[0]);
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Configuration numeric %s Value %d",
                            "MSGSRV_A_SMTP_VERIFY_ADDRESS", CheckRCPT);
                }
                MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_ACCEPT_ETRN, Config)) {
                    AcceptETRN = atol (Config->Value[0]);
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Configuration numeric %s Value %d",
                            "MSGSRV_A_SMTP_ACCEPT_ETRN",  AcceptETRN);
                }
                MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_SEND_ETRN, Config)) {
                    SendETRN = atol (Config->Value[0]);
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Configuration numeric %s Value %d",
                            "MSGSRV_A_SMTP_SEND_ETRN", SendETRN);
                }
                MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_RECIPIENT_LIMIT, Config)) {
                    MaximumRecipients = atol (Config->Value[0]);
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Configuration numeric %s Value %d",
                            "MSGSRV_A_RECIPIENT_LIMIT", MaximumRecipients);
                    if (MaximumRecipients == 0) {
                        MaximumRecipients = ULONG_MAX;
                    }
                }
                else {
                    MaximumRecipients = ULONG_MAX;
                }
                MDBFreeValues (Config);

                MaxNullSenderRecips = ULONG_MAX;
                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_CONFIGURATION, Config) > 0) {
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Config numeric %s Value %s",
                            "MSGSRV_A_CONFIGURATION", Config->Value[i]);

                    for (i = 0; i < Config->Used; i++) {
                        if (XplStrNCaseCmp
                            (Config->Value[i], "MaxFloodCount=", 14) == 0) {
                            MaxFloodCount = atol (Config->Value[i] + 14);
                        }
                        else if (XplStrNCaseCmp
                                 (Config->Value[i], "MaxNullSenderRecips=",
                                  20) == 0) {
                            ptr = Config->Value[i] + 20;
                            while (isspace (*ptr)) {
                                ptr++;
                            }

                            MaxNullSenderRecips = atol (ptr);
                            if (MaxNullSenderRecips == 0) {
                                MaxNullSenderRecips = ULONG_MAX;
                            }
                        }
                    }
                }
                MDBFreeValues (Config);

		if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_UBE_SMTP_AFTER_POP,
			     Config)) {
		    if (Config->Value[0]) {
			UBEConfig |= UBE_SMTP_AFTER_POP;
		    } else {
                        UBEConfig &= ~UBE_SMTP_AFTER_POP;
		    }
		}
		MDBFreeValues (Config);
		if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_UBE_REMOTE_AUTH_ONLY,
			     Config)) {
		    if (Config->Value[0]) {
			UBEConfig |= UBE_REMOTE_AUTH_ONLY;
		    } else {
			UBEConfig &= ~UBE_REMOTE_AUTH_ONLY;
		    }
		}
		MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_RTS_ANTISPAM_CONFIG,
                     Config) > 0) {
                    if (sscanf
                        (Config->Value[0],
                         "Enabled:%d Delay:%ld Threshhold:%lu", &BlockRTSSpam,
                         &BounceInterval, &MaxBounceCount) != 3) {
                        BlockRTSSpam = FALSE;
                    }
                    if ((MaxBounceCount < 1) || (BounceInterval < 1)) {
                        BlockRTSSpam = FALSE;
                    }
                }
                else {
                    BlockRTSSpam = FALSE;
                }
                MDBFreeValues (Config);

                if (MDBRead
                    (MSGSRV_AGENT_SMTP, MSGSRV_A_MESSAGE_LIMIT, Config)) {
                    MessageLimit = atol (Config->Value[0]) * 1024 * 1024;       /* Convert megabytes to bytes */
                    LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                            LOGGER_EVENT_CONFIGURATION_NUMERIC,
                            LOG_INFO, "Configuration numeric",
                            "MSGSRV_A_MESSAGE_LIMIT", MessageLimit);
                }
                MDBFreeValues (Config);

                XplRWWriteLockRelease (&ConfigLock);
            }
        }

        MDBDestroyEnumStruct (ES, Config);
        MDBDestroyValueStruct (Parents);
        MDBDestroyValueStruct (Config);
    }

#if VERBOSE
    XplConsolePrintf ("SMTPD: Configuration monitor thread done.\r\n");
#endif

    XplSafeDecrement (SMTPServerThreads);

    XplExitThread (TSR_THREAD, 0);
}


static BOOL
ReadConfiguration (void)
{
    unsigned char *ptr;
    struct sockaddr_in soc_address;
    struct hostent *he;
    struct sockaddr_in *sin = &soc_address;
    MDBValueStruct *Config;
    MDBValueStruct *Parents;
    MDBEnumStruct *ES;
    int i = 0, j;
    BOOL Exists, Added;


    Config =
        MDBCreateValueStruct (SMTPDirectoryHandle, MsgGetServerDN (NULL));
    Parents =
        MDBCreateValueStruct (SMTPDirectoryHandle, MsgGetServerDN (NULL));

    ES = MDBCreateEnumStruct (Config);

    soc_address.sin_addr.s_addr = XplGetHostIPAddress ();
    sprintf (Hostaddr, "[%d.%d.%d.%d]", sin->sin_addr.s_net,
             sin->sin_addr.s_host, sin->sin_addr.s_lh, sin->sin_addr.s_impno);
    AddDomain (Hostaddr);
    if (strlen (Hostname) < sizeof (OfficialName)) {
        strcpy (OfficialName, Hostname);
    }

    MDBReadDN (MSGSRV_AGENT_SMTP, MSGSRV_A_PARENT_OBJECT, Parents);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_DOMAIN, Config) > 0) {
        for (i = 0; i < Config->Used; i++) {
            AddDomain (Config->Value[i]);
            LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                    LOGGER_EVENT_CONFIGURATION_NUMERIC,
                    LOG_INFO, "Config numeric %s Value %s",
                         "MSGSRV_A_DOMAIN", Config->Value[i]);
        }
    }

    he = gethostbyaddr ((unsigned char *) &(soc_address.sin_addr.s_addr),
                        sizeof (soc_address.sin_addr.s_addr), AF_INET);

    Added = FALSE;
    if (he) {
        Exists = FALSE;
        for (j = 0; j < DomainCount; j++) {
            if (XplStrCaseCmp (Domains[j], he->h_name) == 0) {
                Exists = TRUE;
                break;
            }
        }
        if (!Exists) {
            AddDomain (he->h_name);
            MDBAddValue (he->h_name, Config);
            Added = TRUE;
        }

        if (he->h_aliases) {
            i = 0;
            while (he->h_aliases[i]) {
                Exists = FALSE;
                for (j = 0; j < DomainCount; j++) {
                    if (XplStrCaseCmp (Domains[j], he->h_aliases[i]) == 0) {
                        Exists = TRUE;
                        break;
                    }
                }
                if (!Exists) {
                    AddDomain (he->h_aliases[i]);
                    MDBAddValue (he->h_aliases[i], Config);
                    Added = TRUE;
                }
                i++;
            }
        }
    }

    if (Added) {
        MDBWrite (MSGSRV_AGENT_SMTP, MSGSRV_A_DOMAIN, Config);
    }

    MDBFreeValues (Config);

    for (j = 0; j < Parents->Used; j++) {
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Config numeric %s Value %s",
                     "MSGSRV_A_PARENT_OBJECT", Parents->Value[j]);
        if (MDBRead (Parents->Value[j], MSGSRV_A_DOMAIN, Config)) {
            for (i = 0; i < Config->Used; i++) {
                AddDomain (Config->Value[i]);
                LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                        LOGGER_EVENT_CONFIGURATION_NUMERIC,
                        LOG_INFO, "Config numeric %s Value %s",
                        "MSGSRV_A_DOMAIN", Config->Value[i]);
            }
        }
        MDBFreeValues (Config);
    }

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_USER_DOMAIN, Config) > 0) {
        for (i = 0; i < Config->Used; i++) {
            AddUserDomain (Config->Value[i]);
            LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                    LOGGER_EVENT_CONFIGURATION_NUMERIC,
                    LOG_INFO, "Config numeric %s Value %s",
                    "MSGSRV_A_USER_DOMAIN", Config->Value[i]);
        }
    }
    MDBFreeValues (Config);

    for (j = 0; j < Parents->Used; j++) {
        if (MDBRead (Parents->Value[j], MSGSRV_A_USER_DOMAIN, Config)) {
            for (i = 0; i < Config->Used; i++) {
                AddUserDomain (Config->Value[i]);
                LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                        LOGGER_EVENT_CONFIGURATION_NUMERIC,
                        LOG_INFO, "Config numeric %s Value %s",
                             "MSGSRV_A_USER_DOMAIN", Config->Value[i]);
            }
        }
        MDBFreeValues (Config);
    }

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_RELAY_DOMAIN, Config) > 0) {
        for (i = 0; i < Config->Used; i++) {
            AddRelayDomain (Config->Value[i]);
            LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                    LOGGER_EVENT_CONFIGURATION_NUMERIC,
                    LOG_INFO, "Config numeric %s Value %s",
                         "MSGSRV_A_RELAY_DOMAIN", Config->Value[i]);
        }
    }
    MDBFreeValues (Config);

    for (j = 0; j < Parents->Used; j++) {
        if (MDBRead (Parents->Value[j], MSGSRV_A_RELAY_DOMAIN, Config)) {
            for (i = 0; i < Config->Used; i++) {
                AddRelayDomain (Config->Value[i]);
                LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                        LOGGER_EVENT_CONFIGURATION_NUMERIC,
                        LOG_INFO, "Config numeric %s Value %s",
                        "MSGSRV_A_RELAY_DOMAIN", Config->Value[i]);
            }
        }
        MDBFreeValues (Config);
    }

    if (MDBRead (".", MSGSRV_A_OFFICIAL_NAME, Config) > 0) {
        if (strlen (Config->Value[0]) < sizeof (OfficialName)) {
            strcpy (OfficialName, Config->Value[0]);
        }
        if (strlen (Config->Value[0]) < sizeof (Hostname)) {
            strcpy (Hostname, Config->Value[0]);
        }
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Config numeric %s value %s",
                "MSGSRV_A_OFFICIAL_NAME", Config->Value[0]);
        AddDomain (Hostname);
    }
    MDBFreeValues (Config);

    if (MDBRead (".", MSGSRV_A_SSL_TLS, Config)) {
	if (atol(Config->Value[0])) {
	    AllowClientSSL = TRUE;
	    UseNMAPSSL = FALSE;
	}
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_ALLOW_EXPN, Config)) {
        AllowEXPN = atol (Config->Value[0]);
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Configuration numeric %s Value %d",
                     "MSGSRV_A_SMTP_ALLOW_EXPN", AllowEXPN);
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_ALLOW_VRFY, Config)) {
        AllowVRFY = atol (Config->Value[0]);
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Configuration numeric %s Value %d",
                "MSGSRV_A_SMTP_ALLOW_VRFY", AllowVRFY);
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_VERIFY_ADDRESS, Config)) {
        CheckRCPT = atol (Config->Value[0]);
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Configuration numeric %s Value %d",
                "MSGSRV_A_SMTP_VERIFY_ADDRESS", CheckRCPT);
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_ACCEPT_ETRN, Config)) {
        AcceptETRN = atol (Config->Value[0]);
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Configuration numeric %s Value %d",
                     "MSGSRV_A_SMTP_ACCEPT_ETRN", AcceptETRN);
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_SMTP_SEND_ETRN, Config)) {
        SendETRN = atol (Config->Value[0]);
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Configuration numeric %s Value %d",
                "MSGSRV_A_SMTP_SEND_ETRN", SendETRN);
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_RECIPIENT_LIMIT, Config)) {
        MaximumRecipients = atol (Config->Value[0]);
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Configuration numeric %s Value %d",
                "MSGSRV_A_RECIPIENT_LIMIT", MaximumRecipients);
        if (MaximumRecipients == 0) {
            MaximumRecipients = ULONG_MAX;
        }
    }
    else {
        MaximumRecipients = ULONG_MAX;
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_UBE_SMTP_AFTER_POP, Config)) {
	if (Config->Value[0]) {
	    UBEConfig |= UBE_SMTP_AFTER_POP;
	} else {
	    UBEConfig &= ~UBE_SMTP_AFTER_POP;
	}
    }
    MDBFreeValues (Config);
    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_UBE_REMOTE_AUTH_ONLY, Config)) {
	if (Config->Value[0]) {
	    UBEConfig |= UBE_REMOTE_AUTH_ONLY;
	} else {
	    UBEConfig &= ~UBE_REMOTE_AUTH_ONLY;
	}
    }
    MDBFreeValues (Config);

    if (MDBRead (".", MSGSRV_A_POSTMASTER, Config)) {
        ptr = strrchr (Config->Value[0], '\\');
        if (ptr) {
            if (strlen (ptr + 1) < sizeof (Postmaster)) {
                strcpy (Postmaster, ptr + 1);
            }
        }
        else {
            if (strlen (Config->Value[0]) < sizeof (Postmaster)) {
                strcpy (Postmaster, Config->Value[0]);
            }

        }
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_STRING,
                LOG_INFO, "Configuration string %s Value %s",
                     "MSGSRV_A_POSTMASTER", Postmaster);
    }
    MDBFreeValues (Config);

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_MESSAGE_LIMIT, Config)) {
        MessageLimit = atol (Config->Value[0]) * 1024 * 1024;   /* Convert megabytes to bytes */
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_NUMERIC,
                LOG_INFO, "Configuration numeric %s Value %d",
                     "MSGSRV_A_MESSAGE_LIMIT", MessageLimit);
    }
    MDBFreeValues (Config);

    if (MsgReadIP (MSGSRV_AGENT_SMTP, MSGSRV_A_QUEUE_SERVER, Config)) {
        strcpy (NMAPServer, Config->Value[0]);
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_STRING,
                LOG_INFO, "Configuration string %s Value %s",
                     "MSGSRV_A_QUEUE_SERVER", Config->Value[0]);
    }
    MDBFreeValues (Config);

    if (RelayHost[0] == '\0') {
        if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_RELAYHOST, Config) > 0) {
            strcpy (RelayHost, Config->Value[0]);
            if ((RelayHost[0] != '\0') && (RelayHost[0] != ' ')) {
                UseRelayHost = TRUE;
                LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                        LOGGER_EVENT_CONFIGURATION_STRING,
                        LOG_INFO, "Configuration string %s Value %s",
                             "MSGSRV_A_RELAYHOST", Config->Value[0]);
            }
            else {
                UseRelayHost = FALSE;
            }
        }
        else {
            UseRelayHost = FALSE;
        }
        MDBFreeValues (Config);
    }

    if (MDBRead (MSGSRV_AGENT_SMTP, MSGSRV_A_RTS_ANTISPAM_CONFIG, Config) > 0) {
        LogMsg (LOGGER_SUBSYSTEM_CONFIGURATION,
                LOGGER_EVENT_CONFIGURATION_STRING,
                LOG_INFO, "Configuration string %s Value %s",
                "MSGSRV_A_RTS_ANTISPAM_CONFIG", Config->Value[0]);
        if (sscanf
            (Config->Value[0], "Enabled:%d Delay:%ld Threshhold:%lu",
             &BlockRTSSpam, &BounceInterval, &MaxBounceCount) != 3) {
            BlockRTSSpam = FALSE;
        }
        if ((MaxBounceCount < 1) || (BounceInterval < 1)) {
            BlockRTSSpam = FALSE;
        }
    }
    else {
        BlockRTSSpam = FALSE;
    }
    MDBFreeValues (Config);

    MaxNullSenderRecips = ULONG_MAX;

    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_PORT, Config)>0) {
        SMTPServerPort = atol (Config->Value[0]);
    }
    MDBFreeValues(Config);
    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_SSL_PORT, Config)>0) {
        SMTPServerPortSSL = atol (Config->Value[0]);
    }
    MDBFreeValues(Config);
    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_MAX_LOAD, Config)>0) {
        SMTPMaxThreadLoad = atol (Config->Value[0]);
    }
    MDBFreeValues(Config);
    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_MAX_FLOOD_COUNT, Config)>0) {
        MaxFloodCount = atol (Config->Value[0]);
    }
    MDBFreeValues(Config);
    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_TIMEOUT, Config)>0) {
        SocketTimeout = atol (Config->Value[0]);
    }
    MDBFreeValues(Config);
    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_MAX_NULL_SENDER_RECIPS, Config)>0) {
        MaxNullSenderRecips = atol (Config->Value[0]);
        if (MaxNullSenderRecips == 0) {
            MaxNullSenderRecips = ULONG_MAX;
        }
    }
    MDBFreeValues(Config);
    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_MAX_MX_SERVERS, Config)>0) {
        MaxMXServers = atol (Config->Value[0]);
    }
    MDBFreeValues(Config);
    if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_RELAY_LOCAL_MAIL, Config)>0) {
        if (UseRelayHost) {
            if (atol (Config->Value[0])) {
                RelayLocalMail = TRUE;
            }
        }
        else {
            XplConsolePrintf ("bongosmtp: relay local mail configured without a relay host; ignoring.\r\n");
        }
    }
    MDBFreeValues(Config);
    
    MDBSetValueStructContext (NULL, Config);
    if (MDBRead (MSGSRV_ROOT, MSGSRV_A_ACL, Config) > 0) {
        HashCredential (MsgGetServerDN (NULL), Config->Value[0], NMAPHash);
    }

    MDBDestroyEnumStruct (ES, Config);
    MDBDestroyValueStruct (Parents);
    MDBDestroyValueStruct (Config);

    LocalAddress = MsgGetHostIPAddress ();

    return (TRUE);
}

XplServiceCode (SMTPShutdownSigHandler)

     int XplServiceMain (int argc, char *argv[])
{
    int ccode;
    XplThreadID ID;

    /* Done binding to ports, drop privs permanently */
    if (XplSetEffectiveUser (MsgGetUnprivilegedUser ()) < 0) {
        XplConsolePrintf
            ("bongosmtp: Could not drop to unprivileged user '%s', exiting.\n",
             MsgGetUnprivilegedUser ());
        return 1;
    }

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

            XplConsolePrintf
                ("SMTPD: Unable to create connection pool; shutting down.\r\n");
            return (-1);
        }
    }
    else {
        XplConsolePrintf
            ("SMTPD: Unable to initialize memory manager; shutting down.\r\n");
        return (-1);
    }

    ConnStartup (SocketTimeout, TRUE);

    MDBInit ();
    if ((SMTPDirectoryHandle = (MDBHandle) MsgInit ()) == NULL) {
        XplBell ();
        XplConsolePrintf
            ("\rSMTPD: Invalid directory credentials; exiting!\n");
        XplBell ();

        MemoryManagerClose (MSGSRV_AGENT_SMTP);

        exit (-1);
    }

    CMInitialize (SMTPDirectoryHandle, "SMTP");

    XplRWLockInit (&ConfigLock);

    LogStartup ();

    for (ccode = 1; argc && (ccode < argc); ccode++) {
        if (XplStrNCaseCmp (argv[ccode], "--forwarder=", 12) == 0) {
            UseRelayHost = TRUE;
            strcpy (RelayHost, argv[ccode] + 12);
        }
    }

    ReadConfiguration ();

    if (ServerSocketInit () < 0) {
        XplConsolePrintf ("bongosmtp: Exiting.\n");
        return 1;
    }

    XplBeginCountedThread (&ID, QueueServerStartup, STACKSIZE_Q, NULL, ccode,
                           SMTPServerThreads);
    XplBeginCountedThread (&ID, SmtpdConfigMonitor, STACKSIZE_Q, NULL, ccode,
                           SMTPServerThreads);

    if (AllowClientSSL) {
        SSL_load_error_strings ();
        SSL_library_init ();
        XPLCryptoLockInit ();
        SSLContext = SSL_CTX_new (SSLv23_server_method ());
        SSL_CTX_set_mode (SSLContext, SSL_MODE_AUTO_RETRY);
        SSLClientContext = SSL_CTX_new (SSLv23_client_method ());
        SSL_CTX_set_mode (SSLClientContext, SSL_MODE_AUTO_RETRY);

        if (SSLContext && SSLClientContext) {
            int result;

            if (ClientSSLOptions & SSL_DONT_INSERT_EMPTY_FRAGMENTS) {
                SSL_CTX_set_options (SSLContext,
                                     SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS);
                SSL_CTX_set_options (SSLClientContext,
                                     SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS);
            }
            if (ClientSSLOptions & SSL_ALLOW_CHAIN) {
/* FIXME: OPENSSL
                if ((result =
                     SSL_CTX_use_certificate_chain_file (SSLContext,
                                                         MsgGetTLSCertPath
                                                         (NULL))) > 0) {
                    result =
                        SSL_CTX_use_certificate_chain_file (SSLClientContext,
                                                            MsgGetTLSCertPath
                                                            (NULL));
                }
*/
            }
            else {
                if ((result =
                     SSL_CTX_use_certificate_file (SSLContext,
                                                   MsgGetTLSCertPath (NULL),
                                                   SSL_FILETYPE_PEM)) > 0) {
                    result =
                        SSL_CTX_use_certificate_file (SSLClientContext,
                                                      MsgGetTLSCertPath
                                                      (NULL),
                                                      SSL_FILETYPE_PEM);
                }
            }

            if (result > 0) {
                if ((SSL_CTX_use_PrivateKey_file
                     (SSLContext, MsgGetTLSKeyPath (NULL),
                      SSL_FILETYPE_PEM) > 0)
                    &&
                    (SSL_CTX_use_PrivateKey_file
                     (SSLClientContext, MsgGetTLSKeyPath (NULL),
                      SSL_FILETYPE_PEM) > 0)) {
/* FIXME: OPENSSL
                    if ((SSL_CTX_check_private_key (SSLContext))
                        && (SSL_CTX_check_private_key (SSLClientContext))) {
*/
                        if (ServerSocketSSLInit () >= 0) {
                            /* Done binding to ports, drop privs permanently */
                            if (XplSetRealUser (MsgGetUnprivilegedUser ()) <
                                0) {
                                XplConsolePrintf
                                    ("bongosmtp: Could not drop to unprivileged user '%s', exiting.\n",
                                     MsgGetUnprivilegedUser ());
                                return 1;
                            }
                            XplBeginCountedThread (&ID, SMTPSSLServer,
                                                   STACKSIZE_S, NULL, ccode,
                                                   SMTPServerThreads);
                        }
                        else {
                            AllowClientSSL = FALSE;
                        }
/* FIXME: OPENSSL 
                   }
                    else {
                        XplConsolePrintf
                            ("\rSMTPD: PrivateKey check failed\n");
                        AllowClientSSL = FALSE;
                    }
*/
                }
                else {
                    XplConsolePrintf
                        ("\rSMTPD: Could not load private key\n");
                    AllowClientSSL = FALSE;
                }
            }
            else {
                XplConsolePrintf ("\rSMTPD: Could not load public key\n");
                AllowClientSSL = FALSE;
            }
        }
        else {
            XplConsolePrintf ("\rSMTPD: Could not generate SSL context\n");
            AllowClientSSL = FALSE;
        }
    }

    /* Done binding to ports, drop privs permanently */
    if (XplSetRealUser (MsgGetUnprivilegedUser ()) < 0) {
        XplConsolePrintf
            ("bongosmtp: Could not drop to unprivileged user '%s', exiting.\n",
             MsgGetUnprivilegedUser ());
        return 1;
    }

    /* Management Client Startup */
    if ((ManagementInit (MSGSRV_AGENT_SMTP, SMTPDirectoryHandle))
        &&
        (ManagementSetVariables
         (SMTPManagementVariables,
          sizeof (SMTPManagementVariables) / sizeof (ManagementVariables)))
        &&
        (ManagementSetCommands
         (SMTPManagementCommands,
          sizeof (SMTPManagementCommands) / sizeof (ManagementCommands)))) {
        XplBeginThread (&ID, ManagementServer, DMC_MANAGEMENT_STACKSIZE, NULL,
                        ccode);
    }


    XplStartMainThread (PRODUCT_SHORT_NAME, &ID, SMTPServer, 8192, NULL,
                        ccode);

    XplUnloadApp (XplGetThreadID ());
    return (0);
}
