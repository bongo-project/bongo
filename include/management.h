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

#ifndef BONGO_MANAGEMENT_H
#define BONGO_MANAGEMENT_H

#include <connio.h>
#include <mdb.h>

#define DMC_MANAGER_PORT 21289
#define DMC_MANAGER_SSL_PORT 21389
#define DMC_HASH_SIZE       128

#define DMC_CONNECTION_TIMEOUT (15 * 60)
#define DMC_MANAGEMENT_STACKSIZE (64 * 1024)
#define DMC_REPORT_PRODUCT_VERSION(d, dl) \
{ \
    unsigned long cOuNtOfByTeS; \
    cOuNtOfByTeS = strlen(PRODUCT_NAME) + 2 + strlen(PRODUCT_SHORT_NAME) + 17 + strlen(PRODUCT_DESCRIPTION) + 2; \
    if ((d)&& ((dl) > cOuNtOfByTeS)) { \
	(dl) = sprintf((d), "%s (%s: v%d.%d.%d)\r\n%s\r\n", PRODUCT_NAME, PRODUCT_SHORT_NAME, PRODUCT_MAJOR_VERSION, PRODUCT_MINOR_VERSION, PRODUCT_LETTER_VERSION, PRODUCT_DESCRIPTION); \
    } else { \
	(dl) = cOuNtOfByTeS; \
    } \
}

enum ManagementStates {
    MANAGEMENT_LOADING = 0, 
    MANAGEMENT_INITIALIZING, 
    MANAGEMENT_READY, 
    MANAGEMENT_RUNNING, 
    MANAGEMENT_STOPPING, 
    MANAGEMENT_STOPPED, 
    
    MANAGEMENT_MAX_STATES    
};

typedef BOOL (*DMCReadVariable)(unsigned int Variable, unsigned char *Data, size_t *DataLength);
typedef BOOL (*DMCWriteVariable)(unsigned int Variable, unsigned char *Data, size_t DataLength);
typedef BOOL (*DMCCommand)(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);

typedef struct _ManagementVariable {
    unsigned char *Name;
    unsigned char *Help;

    DMCReadVariable Read;
    DMCWriteVariable Write;
} ManagementVariables;

typedef struct _DMC_MANAGEMENT_COMMAND {
    unsigned char *Name;

    DMCCommand Execute;
} ManagementCommands;

typedef struct _AddressBookStatistics{
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} AddressBookStatistics;

typedef struct _BONGO_ALIAS_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} AliasStatistics;

typedef struct _BONGO_ANTISPAM_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} AntiSPAMStatistics;

typedef struct _BONGO_ANTIVIRUS_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
    unsigned long MessagesScanned; /* AVMV_MESSAGES_SCANNED */
    unsigned long AttachmentsScanned; /* AVMV_ATTACHMENTS_SCANNED */
    unsigned long AttachmentsBlocked; /* AVMV_ATTACHMENTS_BLOCKED */
    unsigned long VirusesFound; /* AVMV_VIRUSES_FOUND */
} AntiVirusStatistics;

typedef struct _BONGO_CALENDAR_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} CalendarStatistics;

typedef struct _BONGO_CAP_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
    unsigned long ConnectionLimit; /* DMCMV_MAX_CONNECTION_COUNT */
} CAPStatistics;

typedef struct _BONGO_CONNECTION_MANAGER_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} ConnectionManagerStatistics;

typedef struct _BONGO_FINGER_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} FingerStatistics;

typedef struct _BONGO_IMAP_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
    unsigned long ConnectionLimit; /* DMCMV_MAX_CONNECTION_COUNT */
    unsigned long ConnectionsServiced; /* DMCMV_TOTAL_CONNECTIONS */
    unsigned long BadPasswords; /* DMCMV_BAD_PASSWORD_COUNT */
} IMAPStatistics;

typedef struct _BONGO_LIST_SERVER_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} ListServerStatistics;

typedef struct _BONGO_NMAP_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
    unsigned long QueueThreads; /* DMCMV_QUEUE_THREAD_COUNT */

    struct {
        BOOL Disabled; /* NMAPMV_LOAD_MONITOR_DISABLED */
        unsigned long Interval; /* NMAPMV_LOAD_MONITOR_INTERVAL */
    } Monitor;

    struct {
        unsigned long Low; /* NMAPMV_LOW_UTILIZATION_THRESHOLD */
        unsigned long High; /* NMAPMV_HIGH_UTILIZATION_THRESHOLD */
    } Utilization;

    struct {
        unsigned long Concurrent; /* NMAPMV_QUEUE_LIMIT_CONCURRENT */
        unsigned long Sequential; /* NMAPMV_QUEUE_LIMIT_SEQUENTIAL */
        unsigned long Threshold; /* NMAPMV_QUEUE_LOAD_THRESHOLD */
    } Queue;

    struct {
        time_t Interval; /* NMAPMV_BOUNCE_INTERVAL */
        unsigned long Maximum; /* NMAPMV_MAX_BOUNCE_COUNT */
    } Bounce;

    struct {
        unsigned long Connections; /* NMAPMV_NMAP_TO_NMAP_CONNECTIONS */
        unsigned long Serviced; /* DMCMV_TOTAL_CONNECTIONS */
        unsigned long Agents; /* NMAPMV_AGENTS_SERVICED */
    } NMAP;

    struct {
        unsigned long Queued; /* NMAPMV_MESSAGES_QUEUED_LOCALLY */
        unsigned long SPAMDiscarded; /* NMAPMV_SPAM_MESSAGES_DISCARDED */

        struct {
            unsigned long Count; /* NMAPMV_MESSAGES_STORED_LOCALLY */
            unsigned long Recipients; /* NMAPMV_RECIPIENTS_STORED_LOCALLY */
            unsigned long KiloBytes; /* NMAPMV_BYTES_STORED_LOCALLY */
        } Stored;

        struct {
            unsigned long Local; /* NMAPMV_LOCAL_DELIVERY_FAILURES */
            unsigned long Remote; /* NMAPMV_REMOTE_DELIVERY_FAILURES */
        } Failed;
    } Messages;
} NMAPStatistics;

typedef struct _BONGO_PLUSPACK_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} PlusPackStatistics;

typedef struct _BONGO_POP3_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
    unsigned long ConnectionLimit; /* DMCMV_MAX_CONNECTION_COUNT */
    unsigned long ConnectionsServiced; /* DMCMV_TOTAL_CONNECTIONS */
    unsigned long BadPasswords; /* DMCMV_BAD_PASSWORD_COUNT */
} POP3Statistics;

typedef struct _BONGO_PROXY_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
    unsigned long ConnectionsServiced; /* DMCMV_TOTAL_CONNECTIONS */
    unsigned long BadPasswords; /* DMCMV_BAD_PASSWORD_COUNT */
    unsigned long Messages; /* PROXYMV_REMOTE_MESSAGES_PULLED */
    unsigned long KiloBytes; /* PROXYMV_REMOTE_BYTES_PULLED */
} ProxyStatistics;

typedef struct _BONGO_RULES_SERVER_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
} RulesServerStatistics;

typedef struct _BONGO_SMTP_STATISTICS {
    unsigned char Version[256]; /* DMCMV_VERSION */

    struct {
        unsigned long Count; /* Connection Pool Allocation Count */
        unsigned long Size; /* Connection Pool Allocation Size */
        unsigned long Pitches; /* Connection Pool Pitches */
        unsigned long Strikes; /* Connection Pool Strikes */
    } Memory;

    unsigned long ServerThreads; /* DMCMV_SERVER_THREAD_COUNT */
    unsigned long Connections; /* DMCMV_CONNECTION_COUNT */
    unsigned long Idle; /* DMCMV_IDLE_CONNECTION_COUNT */
    unsigned long ConnectionLimit; /* DMCMV_MAX_CONNECTION_COUNT */
    unsigned long QueueThreads; /* DMCMV_QUEUE_THREAD_COUNT */
    unsigned long BadPasswords; /* DMCMV_BAD_PASSWORD_COUNT */

    struct {
        struct {
            unsigned long Connections; /* SMTPMV_INCOMING_SERVERS */
            unsigned long Serviced; /* SMTPMV_SERVICED_SERVERS */
        } InBound;

        struct {
            unsigned long Connections; /* SMTPMV_OUTGOING_SERVERS */
            unsigned long Serviced; /* SMTPMV_CLOSED_OUT_SERVERS */
        } OutBound;
    } Servers;

    struct {
        unsigned long Connections; /* SMTPMV_INCOMING_QUEUE_AGENT */
    } NMAP;

    struct {
        unsigned long Address; /* SMTPMV_SPAM_ADDRS_BLOCKED */
        unsigned long MAPS; /* SMTPMV_SPAM_MAPS_BLOCKED */
        unsigned long NoDNSEntry; /* SMTPMV_SPAM_NO_DNS_ENTRY */
        unsigned long Routing; /* SMTPMV_SPAM_DENIED_ROUTING */
        unsigned long Queue; /* SMTPMV_SPAM_QUEUE */
    } Blocked;

    struct {
        unsigned long Count; /* SMTPMV_LOCAL_MSGS_RECEIVED */
        unsigned long Recipients; /* SMTPMV_LOCAL_RECIP_RECEIVED */
        unsigned long KiloBytes; /* SMTPMV_LOCAL_BYTES_RECEIVED */
    } Local;

    struct {
        struct {
            unsigned long Received; /* SMTPMV_REMOTE_RECIP_RECEIVED */
            unsigned long Stored; /* SMTPMV_REMOTE_RECIP_STORED */
        } Recipients;

        struct {
            unsigned long Received; /* SMTPMV_REMOTE_BYTES_RECEIVED */
            unsigned long Stored; /* SMTPMV_REMOTE_BYTES_STORED */
        } KiloBytes;

        struct {
            unsigned long Received; /* SMTPMV_REMOTE_MSGS_RECEIVED */
            unsigned long Stored; /* SMTPMV_REMOTE_MSGS_STORED */
        } Messages;
    } Remote;
} SMTPStatistics;

typedef struct _BongoStatistics {
    unsigned long UpTime;
    unsigned long ModuleCount;

    AddressBookStatistics AddressBook;
    AliasStatistics Alias;
    AntiSPAMStatistics AntiSpam;
    AntiVirusStatistics AntiVirus;
    CalendarStatistics Calendar;
    CAPStatistics CAP;
    ConnectionManagerStatistics ConnectionManager;
    FingerStatistics Finger;
    IMAPStatistics IMAP;
    ListServerStatistics List;
    NMAPStatistics NMAP;
    PlusPackStatistics PlusPack;
    POP3Statistics POP3;
    ProxyStatistics Proxy;
    RulesServerStatistics Rules;
    SMTPStatistics SMTP;
} BongoStatistics;

typedef struct _BONGO_STATISTICS {
    /* Generic */
    unsigned long ModulesLoaded;
    unsigned long Uptime;
    unsigned long SystemLoad;
    unsigned long SystemMemoryUsed;
    unsigned long UnauthorizedAccess;
    unsigned long WrongPassword; /* +    NMStats->POP3.BadPasswords
                                    +    NMStats->IMAP.BadPasswords
                                    +    NMStats->SMTP.BadPasswords
                                    +    NMStats->Proxy.BadPasswords */
    unsigned long DeliveryFailedLocal; /* NMStats->NMAP.Messages.Failed.Local */
    unsigned long DeliveryFailedRemote; /* NMStats->NMAP.Messages.Failed.Remote */

    /* Connections */
    unsigned long IncomingClients; /* +    NMStats->POP3.Connections
                                      +    NMStats->IMAP.Connections */
    unsigned long IncomingServers; /* NMStats->SMTP.Connections */
    unsigned long ServicedClients; /* +    NMStats->POP3.ConnectionsServiced
                                      +    NMStats->IMAP.ConnectionsServiced */
    unsigned long ServicedServers; /* NMStats->SMTP.Servers.InBound.Serviced */
    unsigned long OutgoingServers; /* NMStats->SMTP.Servers.OutBound.Connections */
    unsigned long ClosedOutServers; /* NMStats->SMTP.Servers.OutBound.Serviced */

    /* NMAP Agents */
    unsigned long IncomingQueueAgents; /* NMStats->SMTP.NMAP.Connections */
    unsigned long OutgoingQueueAgents;
    unsigned long CurrentStoreAgents; /* NMStats->NMAP.Connections */
    unsigned long ServicedStoreAgents; /* NMStats->NMAP.NMAP.Agents */
    unsigned long CurrentNMAPtoNMAPAgents; /* NMStats->NMAP.NMAP.Connections */
    unsigned long ServicedNMAPtoNMAPAgents; /* NMStats->NMAP.NMAP.Serviced */

    /* Messages */
    unsigned long MsgReceivedLocal; /* NMStats->SMTP.Local.Count */
    unsigned long MsgReceivedRemote; /* NMStats->SMTP.Remote.Messages.Received */
    unsigned long MsgStoredLocal; /* NMStats->NMAP.Messages.Stored.Count */
    unsigned long MsgStoredRemote; /* NMStats->SMTP.Remote.Messages.Stored */
    unsigned long MsgQueuedLocal; /* NMStats->NMAP.Messages.Queued */
    unsigned long MsgQueuedRemote;

    /* Bytes */
    unsigned long ByteReceivedLocal; /* NMStats->SMTP.Local.KiloBytes */
    unsigned long ByteReceivedRemote; /* NMStats->SMTP.Remote.KiloBytes.Received */
    unsigned long ByteStoredLocal; /* NMStats->NMAP.Messages.Stored.KiloBytes */
    unsigned long ByteStoredRemote; /* NMStats->SMTP.Remote.KiloBytes.Stored */
    unsigned long ByteReceivedCount; /* NMStats->SMTP.Local.Count
                                        +    NMStats->SMTP.Remote.Messages.Received */
    unsigned long ByteStoredCount; /* NMStats->NMAP.Messages.Stored.Count
                                      +    NMStats->SMTP.Remote.KiloBytes.Stored */

    /* Recipients */
    unsigned long RcptReceivedLocal; /* NMStats->SMTP.Local.Recipients */
    unsigned long RcptReceivedRemote; /* NMStats->SMTP.Remote.Recipients.Received */
    unsigned long RcptStoredLocal; /* NMStats->NMAP.Messages.Stored.Recipients */
    unsigned long RcptStoredRemote; /* NMStats->SMTP.Remote.Recipients.Stored */
} StatisticsStruct;

typedef struct _ANTISPAM_STATISTICS {
    unsigned long QueueSpamBlocked; /* NMStats->NMAP.Messages.SPAMDiscared
                                       +    NMStats->SMTP.Blocked.Queue */
    unsigned long AddressBlocked; /* NMStats->SMTP.Blocked.Address */
    unsigned long MAPSBlocked; /* NMStats->SMTP.Blocked.MAPS */
    unsigned long DeniedRouting; /* NMStats->SMTP.Blocked.Routing */
    unsigned long NoDNSEntry; /* NMStats->SMTP.Blocked.NoDNSEntry */
    unsigned long MessagesScanned; /* NMStats->AntiVirus.MessagesScanned */
    unsigned long AttachmentsScanned; /* NMStats->AntiVirus.AttachmentsScanned */
    unsigned long AttachmentsBlocked; /* NMStats->AntiVirus.AttachmentsBlocked */
    unsigned long VirusesFound; /* NMStats->AntiVirus.VirusesFound */
    unsigned long VirusesBlocked;
    unsigned long VirusesCured;
} AntispamStatisticsStruct;

#define NMSTATS_TO_STATS(n, s) \
        if (s) { \
            memset((s), 0, sizeof(StatisticsStruct)); \
        } \
        if (n) { \
            if (s) { \
                (s)->Uptime = (n)->UpTime; \
                (s)->WrongPassword = (n)->POP3.BadPasswords \
                            + (n)->IMAP.BadPasswords \
                            + (n)->SMTP.BadPasswords \
                            + (n)->Proxy.BadPasswords; \
                (s)->DeliveryFailedLocal = (n)->NMAP.Messages.Failed.Local; \
                (s)->DeliveryFailedRemote = (n)->NMAP.Messages.Failed.Remote; \
                (s)->IncomingClients = + (n)->POP3.Connections \
                            + (n)->IMAP.Connections; \
                (s)->IncomingServers = (n)->SMTP.Connections; \
                (s)->ServicedClients = (n)->POP3.ConnectionsServiced \
                            + (n)->IMAP.ConnectionsServiced; \
                (s)->ServicedServers = (n)->SMTP.Servers.InBound.Serviced; \
                (s)->OutgoingServers = (n)->SMTP.Servers.OutBound.Connections; \
                (s)->ClosedOutServers = (n)->SMTP.Servers.OutBound.Serviced; \
                (s)->IncomingQueueAgents = (n)->SMTP.NMAP.Connections; \
                (s)->CurrentStoreAgents = (n)->NMAP.Connections; \
                (s)->ServicedStoreAgents = (n)->NMAP.NMAP.Agents; \
                (s)->CurrentNMAPtoNMAPAgents = (n)->NMAP.NMAP.Connections; \
                (s)->ServicedNMAPtoNMAPAgents = (n)->NMAP.NMAP.Serviced; \
                (s)->MsgReceivedLocal = (n)->SMTP.Local.Count; \
                (s)->MsgReceivedRemote = (n)->SMTP.Remote.Messages.Received; \
                (s)->MsgStoredLocal = (n)->NMAP.Messages.Stored.Count; \
                (s)->MsgStoredRemote = (n)->SMTP.Remote.Messages.Stored; \
                (s)->MsgQueuedLocal = (n)->NMAP.Messages.Queued; \
                (s)->ByteReceivedLocal = (n)->SMTP.Local.KiloBytes; \
                (s)->ByteReceivedRemote = (n)->SMTP.Remote.KiloBytes.Received; \
                (s)->ByteStoredLocal = (n)->NMAP.Messages.Stored.KiloBytes; \
                (s)->ByteStoredRemote = (n)->SMTP.Remote.KiloBytes.Stored; \
                (s)->ByteReceivedCount = (n)->SMTP.Local.Count \
                            + (n)->SMTP.Remote.Messages.Received; \
                (s)->ByteStoredCount = (n)->NMAP.Messages.Stored.Count \
                            + (n)->SMTP.Remote.KiloBytes.Stored; \
                (s)->RcptReceivedLocal = (n)->SMTP.Local.Recipients; \
                (s)->RcptReceivedRemote = (n)->SMTP.Remote.Recipients.Received; \
                (s)->RcptStoredLocal = (n)->NMAP.Messages.Stored.Recipients; \
                (s)->RcptStoredRemote = (n)->SMTP.Remote.Recipients.Stored; \
            } \
        }

#define NMSTATS_TO_SPAMSTATS(n, sp) \
        if (sp) { \
            memset((sp), 0, sizeof(AntispamStatisticsStruct)); \
        } \
        if (n) { \
            if (sp) { \
                (sp)->QueueSpamBlocked    = (n)->NMAP.Messages.SPAMDiscarded \
                            + (n)->SMTP.Blocked.Queue; \
                (sp)->AddressBlocked        = (n)->SMTP.Blocked.Address; \
                (sp)->MAPSBlocked            = (n)->SMTP.Blocked.MAPS; \
                (sp)->DeniedRouting        = (n)->SMTP.Blocked.Routing; \
                (sp)->NoDNSEntry            = (n)->SMTP.Blocked.NoDNSEntry; \
                (sp)->MessagesScanned        = (n)->AntiVirus.MessagesScanned; \
                (sp)->AttachmentsScanned = (n)->AntiVirus.AttachmentsScanned; \
                (sp)->AttachmentsBlocked = (n)->AntiVirus.AttachmentsBlocked; \
                (sp)->VirusesFound            = (n)->AntiVirus.VirusesFound; \
            } \
        }

#define PVCSRevisionToVersion(pvcs, version) \
        { unsigned char *delimPTR; \
            delimPTR = strchr((pvcs), ':'); \
            if (delimPTR) { \
                do { \
                    delimPTR++; \
                } while (isspace(*delimPTR)); \
                strcpy((version), delimPTR); \
                delimPTR = strchr((version), '$'); \
                if (delimPTR) { \
                    do { \
                        delimPTR--; \
                    } while (isspace(*delimPTR)); \
                    delimPTR[1] = '\0'; \
                } else { \
                    strcpy((version), "0.0"); \
                } \
            } else { \
                strcpy((version), "0.0"); \
            } \
        }

/*    Response Codes:

    10XX    Success
    11XX    State

    20XX    Server Errors
    21XX    Requestor Errors
*/
#define DMCMSG1000OK "1000 OK\r\n"
#define DMCMSG1000OK_LEN 9

#define DMCMSG1001RESPONSES_COMMING "1001-%d responses to follow.\r\n"

#define DMCMSG1001BYTES_COMING "1001-%d octets to follow.\r\n"

#define DMCMSG1002SALT_COMING "1002-Salt to follow.\r\n"
#define DMCMSG1002SALT_COMING_LEN 22

#define DMCMSG1002SIGNATURE_COMING "1002-Agent signature to follow.\r\n"
#define DMCMSG1002SIGNATURE_COMING_LEN 33

#define DMCMSG1003SEND_BYTES "1003-Send %d octets.\r\n"

#define DMCMSG1003SEND_VARIABLES "1003-Send variables.\r\n"
#define DMCMSG1003SEND_VARIABLES_LEN 22

#define DMCMSG1003SEND_COMMANDS "1003-Send commands.\r\n"
#define DMCMSG1003SEND_COMMANDS_LEN 21

#define DMCMSG1004NEGOTIATE_TLS "1001-Begin TLS negotiation.\r\n"
#define DMCMSG1004NEGOTIATE_TLS_LEN 29

#define DMCMSG1003SEND_VALUE "1003-Send value.\r\n"
#define DMCMSG1003SEND_VALUE_LEN 18

#define DMCMSG1100USE_AUTH ": Authentication required, use LOGIN\r\n"
#define DMCMSG1100USE_AUTH_LEN 38

#define DMCMSG1101AUTHORIZED ": Authorized [%s]\r\n"

#define DMCMSG1102MANAGING_SERVER ": Managing \"%s\"\r\n"

#define DMCMSG1102MANAGING_AGENT ": Managing \"%s\" [\"%s\"]\r\n"

#define DMCMSG1103BAD_STATE ": Invalid state; use EXIT\r\n"
#define DMCMSG1103BAD_STATE_LEN 27

#define DMCMSG1104SERVER_DEREGISTERED "1104 Server has deregistered.\r\n"
#define DMCMSG1104SERVER_DEREGISTERED_LEN 31

#define DMCMSG1104AGENT_DEREGISTERED "1104 Agent has deregistered.\r\n"
#define DMCMSG1104AGENT_DEREGISTERED_LEN 30

#define DMCMSG2000NOMEMORY "2000 Out of memory.\r\n"
#define DMCMSG2000NOMEMORY_LEN 21

#define DMCMSG2000NOT_IMPLEMENTED "2000 Command not implemented.\r\n"
#define DMCMSG2000NOT_IMPLEMENTED_LEN 31

#define DMCMSG2001NOT_MANAGER "2001 Not a configured manager.\r\n"
#define DMCMSG2001NOT_MANAGER_LEN 32

#define DMCMSG2001NEGOTIATE_FAILED "2001 TLS negotiation failed.\r\n"
#define DMCMSG2001NEGOTIATE_FAILED_LEN 30

#define DMCMSG2002READ_ERROR "2002 Error reading variable value.\r\n"
#define DMCMSG2002READ_ERROR_LEN 36

#define DMCMSG2002WRITE_ERROR "2002 Error writing variable value.\r\n"
#define DMCMSG2002WRITE_ERROR_LEN 36

#define DMCMSG2003CONNECTION_CLOSED "2003 Connection with resource interrupted.\r\n"
#define DMCMSG2003CONNECTION_CLOSED_LEN 44

#define DMCMSG2004SERVER_DOWN "2004 Server shutting down.\r\n"

#define DMCMSG2005SERVER_TOO_MANY "2005 Connection limit reached."

#define DMCMSG2006SERVER_NO_ACCEPT "2006 Connections not accepted at this time."

#define DMCMSG2100BADCOMMAND "2100 Command unrecognized.\r\n"
#define DMCMSG2100BADCOMMAND_LEN 28

#define DMCMSG2100BADRESPONSE "2100 Unexpected resource response of \"%s\".\r\n"

#define DMCMSG2101BADPARAMS "2101 Invalid command parameters.\r\n"
#define DMCMSG2101BADPARAMS_LEN 34

#define DMCMSG2102BADSTATE "2102 Invalid connection state for command.\r\n"
#define DMCMSG2102BADSTATE_LEN 44

#define DMCMSG2103RESET "2103 Already managing; use RESET to stop managing.\r\n"
#define DMCMSG2103RESET_LEN 52

#define DMCMSG2104NOT_WRITEABLE "2104 Variable not settable.\r\n"
#define DMCMSG2104NOT_WRITEABLE_LEN 29

#define DMCMSG2105PORT_DISABLED "2105 No communications port configured.\r\n"
#define DMCMSG2105PORT_DISABLED_LEN 41

#define DMCMSG2106CONNECT_FAILED "2106 Connection attempt failed.\r\n"
#define DMCMSG2106CONNECT_FAILED_LEN 33

#define DMCMSG3242BADAUTH "3242 Bad authentication!\r\n"
#define DMCMSG2106BADAUTH_LEN 26

/*    Common Managed Variables    */
#define DMCMV_CONNECTION_COUNT "Connection Count"
#define DMCMV_CONNECTION_COUNT_HELP "Count of active connection threads.\r\n"

#define DMCMV_IDLE_CONNECTION_COUNT "Idle Connection Count"
#define DMCMV_IDLE_CONNECTION_COUNT_HELP "Count of idle connection threads.\r\n"

#define DMCMV_MAX_CONNECTION_COUNT "Maximum Connections"
#define DMCMV_MAX_CONNECTION_COUNT_HELP "The maximum number of connections allowed.\r\n"

#define DMCMV_SERVER_THREAD_COUNT "Server Thread Count"
#define DMCMV_SERVER_THREAD_COUNT_HELP "Count of active agent server threads.\r\n"

#define DMCMV_QUEUE_THREAD_COUNT "Queue Thread Count"
#define DMCMV_QUEUE_THREAD_COUNT_HELP "Count of active message queue processing threads.\r\n"

#define DMCMV_HOSTNAME "Host Name"
#define DMCMV_HOSTNAME_HELP "The agent's configured host name.\r\n"

#define DMCMV_OFFICIAL_NAME "Official Name"
#define DMCMV_OFFICIAL_NAME_HELP "The official domain name for the agent.\r\n"

#define DMCMV_REVISIONS "Revisions"
#define DMCMV_REVISIONS_HELP "Report the agent's source file revision information.\r\n"

#define DMCMV_VERSION "Version"
#define DMCMV_VERSION_HELP "Report the agent's version information.\r\n"

#define DMCMV_DBF_DIRECTORY "DBF Directory"
#define DMCMV_DBF_DIRECTORY_HELP "The agent's configured DBF directory  {What does DBF stand for?}.\r\n"

#define DMCMV_NLS_DIRECTORY "NLS Directory"
#define DMCMV_NLS_DIRECTORY_HELP "The agent's configured NLS directory  {what does NLS stand for?}.\r\n"

#define DMCMV_MAIL_DIRECTORY "Mail Directory"
#define DMCMV_MAIL_DIRECTORY_HELP "The agent's configured Mail directory.\r\n"

#define DMCMV_SCMS_DIRECTORY "SCMS Directory"
#define DMCMV_SCMS_DIRECTORY_HELP "The agent's configured Single Copy Message Store directory.\r\n"

#define DMCMV_SPOOL_DIRECTORY "Spool Directory"
#define DMCMV_SPOOL_DIRECTORY_HELP "The agent's configured Spool directory.\r\n"

#define DMCMV_WORK_DIRECTORY "Work Directory"
#define DMCMV_WORK_DIRECTORY_HELP "The configured working directory used by the agent.\r\n"

#define DMCMV_NMAP_QUEUE "Registered NMAP Queue"
#define DMCMV_NMAP_QUEUE_HELP "The NMAP Queue the agent registered for.\r\n"

#define DMCMV_NMAP_ADDRESS "NMAP Address"
#define DMCMV_NMAP_ADDRESS_HELP "The address of the NMAP server to which the agent registered.\r\n"

#define DMCMV_NOTIFY_POSTMASTER "Notify Postmaster"

#define DMCMV_POSTMASTER "PostMaster"
#define DMCMV_POSTMASTER_HELP "The messaging user desginated as the PostMaster.\r\n"

#define DMCMV_RECEIVER_DISABLED "Block Connections"
#define DMCMV_RECEIVER_DISABLED_HELP "Set to TRUE to block connection attempts to the agent.\r\n"

#define DMCMV_MESSAGE "Message"

#define DMCMV_SESSION_COUNT "Session Count"
#define DMCMV_SESSION_COUNT_HELP "Number of active sessions.\r\n"

#define DMCMV_TOTAL_CONNECTIONS "Connections Serviced"
#define DMCMV_TOTAL_CONNECTIONS_HELP "Number of connections serviced by the agent.\r\n"

#define DMCMV_BAD_PASSWORD_COUNT "Bad Passwords"
#define DMCMV_BAD_PASSWORD_COUNT_HELP "Number of bad passwords received by the agent.\r\n"

#define DMCMV_UTC_OFFSET "UTC Offset"
#define DMCMV_UTC_OFFSET_HELP "Universal Time-Coordinated offset used by the agent.\r\n"

#define DMCMV_SSL_ENABLED "Allow SSL"
#define DMCMV_SSL_ENABLED_HELP "Support SSL connections.\r\n"

#define DMCMV_PORT "Port"
#define DMCMV_PORT_HELP "TCP port used to receive requests.\r\n"

#define DMCMV_SSL_PORT "SSL Port"
#define DMCMV_SSL_PORT_HELP "TCP port used to receive SSL or TLS encrypted requests.\r\n"

#define DMCMV_MESSAGING_SERVER_DN "Server DN"
#define DMCMV_MESSAGING_SERVER_DN_HELP "The complete directory name for the messaging server object.\r\n"

#define DMCMV_CLIENT_POOL_STATISTICS "Client Pool Statistics"
#define DMCMV_CLIENT_POOL_STATISTICS_HELP "Client memory pool usage statistics"

#define DMCMV_MEM_STATISTICS "Mem Statistics"
#define DMCMV_MEM_STATISTICS_HELP "Memory Manager usage statistics"

/*    Common Managed Commands    */
#define DMCMC_UNKOWN_COMMAND "Unknown agent command.\r\n"

#define DMCMC_HELP "Help"
#define DMCMC_HELP_HELP "Help        Stats       Unload\r\n"

#define DMCMC_SHUTDOWN "Shutdown"
#define DMCMC_SHUTDOWN_HELP "Initiate an agent shutdown and automatic unload of the module.\r\n"

#define DMCMC_STATS "Stats"
#define DMCMC_STATS_HELP "Agent name, version and current values of statistics variables.\r\n"

#define DMCMC_DUMP_MEMORY_USAGE "Memory"
#define DMCMC_DUMP_MEMORY_USAGE_HELP "Summary of memory usage by address space.\r\n"

#define DMCMC_CONN_TRACE_USAGE "ConnTrace"
#define DMCMC_CONN_TRACE_USAGE_HELP "Used to designate which network events should be traced. \r\n\r\nUsage: 'CMD CONNTRACE FLAG <+|-><flag> [ <+|-><flag>...]'\r\n        where flags are:\r\n            ALL, OUTBOUND, INBOUND, NMAP, CONNECT, CLOSE,\r\n            SSLCONNECT, SSLSHUTDOWN, READ, WRITE, ERROR\r\n\r\nUsage: 'CMD CONNTRACE ADDR (<+|->ALL | <+|-><IP ADDRESS> ...)'\r\n"

/*    Address Book Managed Variables    */
#define ADBKMV_LDAP_LISTEN_PORT "LDAP Port"
#define ADBKMV_LDAP_LISTEN_PORT_HELP "Agent TCP port to receive LDAP requests.\r\n"

#define ADBKMV_LDAP_SSL_LISTEN_PORT "LDAP SSL Port"
#define ADBKMV_LDAP_SSL_LISTEN_PORT_HELP "Agent TCP port to receive LDAP SSL encrypted requests.\r\n"

#define ADBKMV_LDIF_DATABASE "LDIF Database"
#define ADBKMV_LDIF_DATABASE_HELP "The agent's LDIF database filename.\r\n"

#define ADBKMV_LDIF_WORKBASE "LDIF Workbase"
#define ADBKMV_LDIF_WORKBASE_HELP "The agent's LDIF work database filename.\r\n"

#define ADBKMV_DBCYCLE_TIME "Database Interval"
#define ADBKMV_DBCYCLE_TIME_HELP "The agent's interval between directory dredges.\r\n"

#define ADBKMV_DO_LDAP "LDAP Enabled"
#define ADBKMV_DO_LDAP_HELP "Enable the agent's LDAP server.\r\n"

#define ADBKMV_DO_LDIF "LDIF Enabled"
#define ADBKMV_DO_LDIF_HELP "Enable the agent's LDIF export.\r\n"

#define ADBKMV_DO_PERSONAL "Personal Lookup Enabled"
#define ADBKMV_DO_PERSONAL_HELP "Enable personal address book entry lookup.\r\n"

#define ADBKMV_LDAP_REQUIRE_BASEDN "Require Base Name"
#define ADBKMV_LDAP_REQUIRE_BASEDN_HELP "Require a base name for LDAP searches.\r\n"

#define ADBKMV_LDAP_REQUIRE_AUTH "Require LDAP Authentication"
#define ADBKMV_LDAP_REQUIRE_AUTH_HELP "Require LDAP authenticate for LDAP searches.\r\n"

#define ADBKMV_USE_USER_BASEDN "Use User Base Name"
#define ADBKMV_USE_USER_BASEDN_HELP "Use the user's base name as the LDAP base name.\r\n"

/*    Alias Managed Variables    */
#define ALASMV_DO_FIRSTLAST "Enable FirstLast"
#define ALASMV_DO_FIRSTLAST_HELP "Enable creation of \"FirstLast\" alias names.\r\n"

#define ALASMV_DO_FLASTNAME "Enable FLastname"
#define ALASMV_DO_FLASTNAME_HELP "Enable creation of \"FLastname\" alias names.\r\n"

#define ALASMV_DO_FIRSTDLAST "Enable FirstDLast"
#define ALASMV_DO_FIRSTDLAST_HELP "Enable creation of \"FirstDLast\" alias names.\r\n"

#define ALASMV_DO_FULLDOT "Enable FullDot"
#define ALASMV_DO_FULLDOT_HELP "Enable creation of \"FullDot\" alias names.\r\n"

#define ALASMV_DO_FULLUNDER "Enable FullUnder"
#define ALASMV_DO_FULLUNDER_HELP "Enable creation of \"FullUnder\" alias names.\r\n"

#define ALASMV_AUTO_POPULATE "Enable AutoPopulation"
#define ALASMV_AUTO_POPULATE_HELP "Enable the auto population of messaging users directory email address.\r\n"

#define ALASMV_POPULATE_DEFAULT "Enable Populate Defaults"
#define ALASMV_POPULATE_DEFAULT_HELP "Use the messaging username and official domain as the directory object's email address.\r\n"

#define ALASMV_POPULATE_FIRSTLAST "Enable Populate FirstLast"
#define ALASMV_POPULATE_FIRSTLAST_HELP "Populate directory entries with \"FirstLast\".\r\n"

#define ALASMV_POPULATE_FLASTNAME "Enable Populate FLastname"
#define ALASMV_POPULATE_FLASTNAME_HELP "Populate directory entries with \"FLastname\".\r\n"

#define ALASMV_POPULATE_FIRSTDLAST "Enable Populate FirstDLast"
#define ALASMV_POPULATE_FIRSTDLAST_HELP "Populate directory entries with \"FirstDLast\".\r\n"

#define ALASMV_POPULATE_FULLDOT "Enable Populate FullDot"
#define ALASMV_POPULATE_FULLDOT_HELP "Populate directory entries with \"FullDot\".\r\n"

#define ALASMV_POPULATE_FULLUNDER "Enable Populate FullUnder"
#define ALASMV_POPULATE_FULLUNDER_HELP "Populate directory entries with \"FullUnder\".\r\n"

#define ALASMV_DBCYCLE_TIME "Database Interval"
#define ALASMV_DBCYCLE_TIME_HELP "The agent's interval in seconds between directory dredges.\r\n"

/* AntiSPAM Managed Variables    */
#define ASPMMV_DO_RTS "Enable RTS"
#define ASPMMV_DO_RTS_HELP "Enable return SPAM to sender.\r\n"

#define ASPMMV_NOTIFY_POSTMASTER "Notify the PostMaster of a SPAM message.\r\n"

#define ASPMMV_BLACK_LIST_ARRAY "BlackList"
#define ASPMMV_BLACK_LIST_ARRAY_HELP "The agent's current list of known SPAM hosts.\r\n"

/*    AntiVirus Managed Variables    */
#define AVMV_DO_INCOMING "Enable Incoming"
#define AVMV_DO_INCOMING_HELP "Enable anti-virus scanning of all incoming messages.\r\n"

#define AVMV_NOTIFY_SENDER "Notify Sender"
#define AVMV_NOTIFY_SENDER_HELP "Notify sender of virus infected messages.\r\n"

#define AVMV_NOTIFY_RECIPIENT "Notify Recipient"
#define AVMV_NOTIFY_RECIPIENT_HELP "Notify recipient of virus infected messages.\r\n"

#define AVMV_CARRIER_SCAN_HOST "Carrier Scan Host"
#define AVMV_CARRIER_SCAN_HOST_HELP "TCP/IP address of the anti-virus scanner.\r\n"

#define AVMV_CARRIER_SCAN_PORT "Carrier Scan Port"
#define AVMV_CARRIER_SCAN_PORT_HELP "UDP port of the anti-virus scanner.\r\n"

#define AVMV_WORK_DIRECTORY "Work Directory"
#define AVMV_WORK_DIRECTORY_HELP "The agent's configured working directory.\r\n"

#define AVMV_PATTERN_FILE_DIRECTORY "Pattern File Directory"
#define AVMV_PATTERN_FILE_DIRECTORY_HELP "The agent's pattern file directory for the anti-virus scanner.\r\n"

#define AVMV_MESSAGES_SCANNED "Messages Scanned"
#define AVMV_MESSAGES_SCANNED_HELP "The number of messages sent to the anti-virus scanner.\r\n"

#define AVMV_ATTACHMENTS_SCANNED "Attachments Scanned"
#define AVMV_ATTACHMENTS_SCANNED_HELP "The number of attachments sent to the anti-virus scanner.\r\n"

#define AVMV_ATTACHMENTS_BLOCKED "Attachments Blocked"
#define AVMV_ATTACHMENTS_BLOCKED_HELP "The number of attachments blocked due to virus infections.\r\n"

#define AVMV_VIRUSES_FOUND "Virus Count"
#define AVMV_VIRUSES_FOUND_HELP "The number of virus infections encountered.\r\n"

/*    Finger Managed Variables    */
#define FNGRMV_FINGER_MESSAGE_HELP "The default finger message.\r\n"

/*    Forward Managed Variables    */
#define FWDMV_FORWARD_MESSAGE_HELP "The default forward message.\r\n"

/*    Connection Manager Managed Variables    */
#define CMMV_MAX_AGE "Maximum Age"
#define CMMV_MAX_AGE_HELP "The maximum time in seconds to retain entries in the connection table.\r\n"
    
/*    IMAP Managed Variables    */
#define IMAPMV_ACL_ENABLED "ACL Supported"
#define IMAPMV_ACL_ENABLED_HELP "Set to TRUE if the IMAP ACL extension is supported.\r\n"

#define IMAPMV_MANAGEMENT_URL "Management URL"
#define IMAPMV_MANAGEMENT_URL_HELP "Account URL reported in the IMAP Netscape extension command.\r\n"

/*    List Server Managed Variables    */
#define LISTMV_DIGEST_HOUR "Digest Hour"
#define LISTMV_DIGEST_HOUR_HELP "The hour to begin digest enumeration through the directory (24 hour format).\r\n"

#define LISTMV_SERVER_NAME "List Server Name"
#define LISTMV_SERVER_NAME_HELP "The server name used by the list.\r\n"

#define LISTMV_BOUNCE_NAME "List Bounce Name"
#define LISTMV_BOUNCE_NAME_HELP "The bounce name used by the list.\r\n"

#define LISTMV_MODERATOR_NAME "List Moderator Name"
#define LISTMV_MODERATOR_NAME_HELP "The moderator name used by the list.\r\n"

#define LISTMV_CONTROL_NAME "List Control Name"
#define LISTMV_CONTROL_NAME_HELP "The control name used by the list.\r\n"

/*    NMAP Managed Variables    */
#define NMAPMV_QUEUE_SLEEP_INTERVAL "Queue Sleep Interval"
#define NMAPMV_QUEUE_SLEEP_INTERVAL_HELP "The maximum number of seconds to sleep before restarting after completing a pass through the queue.\r\n"

#define NMAPMV_BYTES_PER_BLOCK "Bytes Per Block"
#define NMAPMV_BYTES_PER_BLOCK_HELP "The number of bytes per block for the mail store on this host.\r\n"

#define NMAPMV_TRUSTED_HOSTS "Trusted Hosts"
#define NMAPMV_TRUSTED_HOSTS_HELP "The list of IP addresses trusted by the agent.\r\n"

#define NMAPMV_TRUSTED_HOST_COUNT "Trusted Hosts Count"
#define NMAPMV_TRUSTED_HOST_COUNT_HELP "The count of trusted hosts for the agent.\r\n"

#define NMAPMV_SCMS_USER_THRESHOLD "SCMS Recipient Threshold"
#define NMAPMV_SCMS_USER_THRESHOLD_HELP "The minimum number of recipients required to consider SCMS usage.\r\n"

#define NMAPMV_SCMS_SIZE_THRESHOLD "SCMS Size Threshold"
#define NMAPMV_SCMS_SIZE_THRESHOLD_HELP "The minimum message size required to consider SCMS usage.\r\n"

#define NMAPMV_USE_SYSTEM_QUOTA "Enable System Quota"
#define NMAPMV_USE_SYSTEM_QUOTA_HELP "Enforce the configured system quota for users with no quota.\r\n"

#define NMAPMV_USE_USER_QUOTA "Enable User Quota"
#define NMAPMV_USE_USER_QUOTA_HELP "Set to TRUE to enforce the user's configured quota.\r\n"

#define NMAPMV_QUOTA_MESSAGE "Quota Message"
#define NMAPMV_QUOTA_MESSAGE_HELP "The message that will be sent when a quota is reached.\r\n"

#define NMAPMV_QUOTA_WARNING "Quota Warning"
#define NMAPMV_QUOTA_WARNING_HELP "The message sent when a user is within 10 percent of the quota limit.\r\n"

#define NMAPMV_MINIMUM_FREE_SPACE "Minimum Free Space"
#define NMAPMV_MINIMUM_FREE_SPACE_HELP "The minimum amount of free space in bytes before rejecting incoming messages.\r\n"

#define NMAPMV_DISK_SPACE_LOW "Disk Space Low"
#define NMAPMV_DISK_SPACE_LOW_HELP "Set to TRUE to indicate that a message queue is low on disk space.\r\n"

#define NMAPMV_DO_DEFERRED_DELIVERY "Enable Deferred Delivery"
#define NMAPMV_DO_DEFERRED_DELIVERY_HELP "Set to TRUE to enable support for deferred delivery during the configured time of day.\r\n"

#define NMAPMV_LOAD_MONITOR_DISABLED "Disable Load Monitor"
#define NMAPMV_LOAD_MONITOR_DISABLED_HELP "Set to TRUE to disable the NMAP load monitor.\r\n"

#define NMAPMV_LOAD_MONITOR_INTERVAL "Load Monitor Interval"
#define NMAPMV_LOAD_MONITOR_INTERVAL_HELP "The number of seconds between checking the host load.\r\n"

#define NMAPMV_LOW_UTILIZATION_THRESHOLD "Low Utilization Threshold"
#define NMAPMV_LOW_UTILIZATION_THRESHOLD_HELP "The low utilization watermark used by the load monitor.\r\n"

#define NMAPMV_HIGH_UTILIZATION_THRESHOLD "High Utilization Threshold"
#define NMAPMV_HIGH_UTILIZATION_THRESHOLD_HELP "The high utilization watermark used by the load monitor.\r\n"

#define NMAPMV_QUEUE_LOAD_THRESHOLD "Queue Threshold"
#define NMAPMV_QUEUE_LOAD_THRESHOLD_HELP "The maximum number of queued messages before entering sequential message processing.\r\n"

#define NMAPMV_NEW_SHARE_MESSAGE "Share Message"
#define NMAPMV_NEW_SHARE_MESSAGE_HELP "The message that will be sent to a user that is granted a shared resource.\r\n"

#define NMAPMV_USER_NAMESPACE_PREFIX "User NameSpace"
#define NMAPMV_USER_NAMESPACE_PREFIX_HELP "The IMAP user namespace extension prefix.\r\n"

#define NMAPMV_QUEUE_LIMIT_CONCURRENT "Concurrent Limit"
#define NMAPMV_QUEUE_LIMIT_CONCURRENT_HELP "The concurrent thread limit for queued message processing.\r\n"

#define NMAPMV_QUEUE_LIMIT_SEQUENTIAL "Sequential Limit"
#define NMAPMV_QUEUE_LIMIT_SEQUENTIAL_HELP "The sequential thread limit for queued message processing.\r\n"

#define NMAPMV_RTS_HANDLING "RTS Handling"
#define NMAPMV_RTS_HANDLING_HELP "The current RTS handling flags.  Flag value of '1' indicates the message should be returned; flag value '2' indicates the message should be copied to the PostMaster.\r\n"

#define NMAPMV_RTS_MAX_BODY_SIZE "RTS Body Size"
#define NMAPMV_RTS_MAX_BODY_SIZE_HELP "The maximum number of bytes in the message body to be copied into a message that is returned to the sender.\r\n"

#define NMAPMV_FORWARD_UNDELIVERABLE_ADDR "Forward Undeliverable Address"
#define NMAPMV_FORWARD_UNDELIVERABLE_ADDR_HELP "The address to which forward undeliverable messages should be sent.\r\n"

#define NMAPMV_FORWARD_UNDELIVERABLE "Forward Undeliverable"
#define NMAPMV_FORWARD_UNDELIVERABLE_HELP "Set to TRUE to forward undeliverable messages.\r\n"

#define NMAPMV_BLOCK_RTS_SPAM "Block RTS SPAM"
#define NMAPMV_BLOCK_RTS_SPAM_HELP "Set to TRUE to discard queued return to sender messages.\r\n"

#define NMAPMV_BOUNCE_INTERVAL "Bounce Interval"
#define NMAPMV_BOUNCE_INTERVAL_HELP "The interval used when determining if an excessive number of bounce messages are being queued.\r\n"

#define NMAPMV_MAX_BOUNCE_COUNT "Max Bounce Count"
#define NMAPMV_MAX_BOUNCE_COUNT_HELP "The maximum number of bounce messages to queue during the bounce interval.\r\n"

#define NMAPMV_NMAP_TO_NMAP_CONNECTIONS "NMAP to NMAP"
#define NMAPMV_NMAP_TO_NMAP_CONNECTIONS_HELP "The count of current NMAP to NMAP connections.\r\n"

#define NMAPMV_BYTES_STORED_LOCALLY "Bytes Stored Locally"
#define NMAPMV_BYTES_STORED_LOCALLY_HELP "Number of kilobytes stored in the local mail store.\r\n"

#define NMAPMV_MESSAGES_STORED_LOCALLY "Messages Stored"
#define NMAPMV_MESSAGES_STORED_LOCALLY_HELP "The count of messages stored in the local mail store.\r\n"

#define NMAPMV_RECIPIENTS_STORED_LOCALLY "Recipients Stored"
#define NMAPMV_RECIPIENTS_STORED_LOCALLY_HELP "The count of recipients stored in the local mail store.\r\n"

#define NMAPMV_MESSAGES_QUEUED_LOCALLY "Messages Queued"
#define NMAPMV_MESSAGES_QUEUED_LOCALLY_HELP "The count of messages queued in the spool directory.\r\n"

#define NMAPMV_SPAM_MESSAGES_DISCARDED "SPAM Discarded"
#define NMAPMV_SPAM_MESSAGES_DISCARDED_HELP "The count of SPAM messages discarded.\r\n"

#define NMAPMV_LOCAL_DELIVERY_FAILURES "Delivery Failures"
#define NMAPMV_LOCAL_DELIVERY_FAILURES_HELP "The count of failed local delivery attempts.\r\n"

#define NMAPMV_REMOTE_DELIVERY_FAILURES "Remote Delivery Failures"
#define NMAPMV_REMOTE_DELIVERY_FAILURES_HELP "The count of failed remote delivery attempts.\r\n"

#define NMAPMV_REGISTERED_AGENTS_ARRAY "Registered Agents"
#define NMAPMV_REGISTERED_AGENTS_ARRAY_HELP "The currently registered NMAP agents.\r\n"

#define NMAPMV_AGENTS_SERVICED "Agents Serviced"
#define NMAPMV_AGENTS_SERVICED_HELP "The number of NMAP agents serviced.\r\n"

/*    PlusPack Managed Variables    */
#define PPACKMV_SIGNATURE "Signature"
#define PPACKMV_SIGNATURE_HELP "The text signature to be appended to all outbound messages.\r\n"

#define PPACKMV_HTML_SIGNATURE "HTML Signature"
#define PPACKMV_HTML_SIGNATURE_HELP "The HTML signature to be appended to all outbound messages.\r\n"

#define PPACKMV_BIGBROTHER_USER "Global Message Copy User Name"
#define PPACKMV_BIGBROTHER_USER_HELP "The name of the messaging user to receive copied messages.\r\n"

#define PPACKMV_BIGBROTHER_USE_MAILBOX "Use Global Message Copy"
#define PPACKMV_BIGBROTHER_USE_MAILBOX_HELP "Set to TRUE to copy all outbound messages to another mailbox.\r\n"

#define PPACKMV_BIGBROTHER_MAILBOX "Global Message Copy Mailbox"
#define PPACKMV_BIGBROTHER_MAILBOX_HELP "The mailbox to which all inbound messages will be copied.\r\n"

#define PPACKMV_BIGBROTHER_SENTBOX "Global Message Copy Sent Items Mailbox"
#define PPACKMV_BIGBROTHER_SENTBOX_HELP "The mailbox to which all outbound messages will be copied.\r\n"

#define PPACKMV_ACL_GROUP "Authorized Group"
#define PPACKMV_ACL_GROUP_HELP "The eDirectory group containing user membership for external message delivery.\r\n"

/*    POP3 Managed Variables    */
#define POP3MV_MANAGEMENT_URL "Management URL"
#define POP3MV_MANAGEMENT_URL_HELP "Account URL listed in the POP3 account administration URL.\r\n"

/*    Proxy Managed Variables    */
#define PROXMV_MAXIMUM_ACCOUNTS "Maximum Accounts"
#define PROXMV_MAXIMUM_ACCOUNTS_HELP "The maximum number of accounts a user can configure for mail proxy.\r\n"

#define PROXMV_MAXIMUM_PARALLEL_THREADS "Maximum Threads"
#define PROXMV_MAXIMUM_PARALLEL_THREADS_HELP "The maximum number of simultaneous mail proxies.\r\n"

#define PROXMV_CONNECTION_TIMEOUT "Timeout"
#define PROXMV_CONNECTION_TIMEOUT_HELP "The maximum number of seconds to wait for a reply from a remote server while attempting to proxy messages.\r\n"

#define PROXMV_RUN_INTERVAL "Interval"
#define PROXMV_RUN_INTERVAL_HELP "The number of seconds between proxy enumerations.  If the current enumeration exceeds the interval length, the next enumeration begins upon the conclusion of the current enumeration.\r\n"

#define PROXYMV_REMOTE_MESSAGES_PULLED "Remote Messages Retrieved"
#define PROXYMV_REMOTE_MESSAGES_PULLED_HELP "The number of remote messages retrieved.\r\n"

#define PROXYMV_REMOTE_BYTES_PULLED "Remote Kilo-Bytes Stored"
#define PROXYMV_REMOTE_BYTES_PULLED_HELP "The number of kilo-bytes of remote messages delivered.\r\n"

/*    Rule Server Managed Variables    */
#define RULEMV_USE_USER_RULES "User Rules"
#define RULEMV_USE_USER_RULES_HELP "Set to TRUE to process user configured rules.\r\n"

#define RULEMV_USE_SYSTEM_RULES "System Rules"
#define RULEMV_USE_SYSTEM_RULES_HELP "Set to TRUE to process system configured rules.\r\n"

/*    SMTP Managed Variables    */
#define SMTPMV_INCOMING_QUEUE_AGENT "Incoming Queue Agent"
#define SMTPMV_INCOMING_QUEUE_AGENT_HELP "The number of incoming NMAP connections being serviced.\r\n"

#define SMTPMV_SERVICED_SERVERS "SMTP Servers"
#define SMTPMV_SERVICED_SERVERS_HELP "The total number of SMTP server connections that have been serviced.\r\n"

#define SMTPMV_INCOMING_SERVERS "Incoming Server"
#define SMTPMV_INCOMING_SERVERS_HELP "The number of cumulative SMTP connections.\r\n"

#define SMTPMV_CLOSED_OUT_SERVERS "NMAP Servers"
#define SMTPMV_CLOSED_OUT_SERVERS_HELP "The total number of NMAP server connections that have been serviced.\r\n"

#define SMTPMV_OUTGOING_SERVERS "Outgoing Servers"
#define SMTPMV_OUTGOING_SERVERS_HELP "The number of remote SMTP connections being used.\r\n"

#define SMTPMV_SPAM_ADDRS_BLOCKED "SPAM Addresses Blocked"
#define SMTPMV_SPAM_ADDRS_BLOCKED_HELP "The number of SMTP connections blocked using the Blocked Addresses list.\r\n"

#define SMTPMV_SPAM_MAPS_BLOCKED "SPAM MAPS Blocked"
#define SMTPMV_SPAM_MAPS_BLOCKED_HELP "The number of SMTP connections blocked using the RBL List.\r\n"

#define SMTPMV_SPAM_NO_DNS_ENTRY "SPAM No DNS Entry"
#define SMTPMV_SPAM_NO_DNS_ENTRY_HELP "The number of SMTP connection blocked due to missing reverse DNS entries.\r\n"

#define SMTPMV_SPAM_DENIED_ROUTING "SPAM Denied Routing"
#define SMTPMV_SPAM_DENIED_ROUTING_HELP "The number of SMTP relay attempts blocked.\r\n"

#define SMTPMV_SPAM_QUEUE "SPAM Queue"
#define SMTPMV_SPAM_QUEUE_HELP "The number of discarded RTS messages.\r\n"

#define SMTPMV_REMOTE_RECIP_RECEIVED "Remote Recipient Received"
#define SMTPMV_REMOTE_RECIP_RECEIVED_HELP "The number of remote recipients received for delivery.\r\n"

#define SMTPMV_LOCAL_RECIP_RECEIVED "Local Recipient Received"
#define SMTPMV_LOCAL_RECIP_RECEIVED_HELP "The number of local recipients received for delivery.\r\n"

#define SMTPMV_REMOTE_RECIP_STORED "Remote Recipient Stored"
#define SMTPMV_REMOTE_RECIP_STORED_HELP "The number of remote recipients delivered.\r\n"

#define SMTPMV_REMOTE_BYTES_RECEIVED "Remote Kilo-Bytes Received"
#define SMTPMV_REMOTE_BYTES_RECEIVED_HELP "The number of kilobytes of messages received for remote delivery.\r\n"

#define SMTPMV_LOCAL_BYTES_RECEIVED "Local Kilo-Bytes Received"
#define SMTPMV_LOCAL_BYTES_RECEIVED_HELP "The number of kilobytes of messages received for local delivery.\r\n"

#define SMTPMV_REMOTE_BYTES_STORED "Remote Kilo-Bytes Stored"
#define SMTPMV_REMOTE_BYTES_STORED_HELP "The number of kilobytes of remote messages delivered.\r\n"

#define SMTPMV_REMOTE_MSGS_RECEIVED "Remote Messages Received"
#define SMTPMV_REMOTE_MSGS_RECEIVED_HELP "The number of messages received for remote delivery.\r\n"

#define SMTPMV_LOCAL_MSGS_RECEIVED "Local Messages Received"
#define SMTPMV_LOCAL_MSGS_RECEIVED_HELP "The number of messages received for local delivery.\r\n"

#define SMTPMV_REMOTE_MSGS_STORED "Remote Messages Stored"
#define SMTPMV_REMOTE_MSGS_STORED_HELP "The number of remote messages delivered.\r\n"

#define SMTPMV_MAX_MX_SERVERS "Maximum MX Servers"
#define SMTPMV_MAX_MX_SERVERS_HELP "The maximum number of MX servers to process before aborting delivery.\r\n"

#define SMTPMV_MAX_BOUNCE_COUNT "Maximum Bounce Count"
#define SMTPMV_MAX_BOUNCE_COUNT_HELP "The maximum number of RTS messages permitted per bounce interval.\r\n"

#define SMTPMV_BOUNCE_COUNT "Bounce Count"
#define SMTPMV_BOUNCE_COUNT_HELP "The current number of RTS messages received for this bounce interval.\r\n"

#define SMTPMV_MESSAGE_LIMIT "Message Limit"
#define SMTPMV_MESSAGE_LIMIT_HELP "The maximum allowed message size.\r\n"

#define SMTPMV_MAX_RECIPS "Maximum Recipients"
#define SMTPMV_MAX_RECIPS_HELP "The maximum allowed recipients per message.\r\n"

#define SMTPMV_MAX_NULL_SENDER_RECIPS "Maximum NULL Sender Recipients"
#define SMTPMV_MAX_NULL_SENDER_RECIPS_HELP "The maximum number of recipients per RTS message.\r\n"

#define SMTPMV_LOCAL_IP_ADDRESS "Local IP Address"
#define SMTPMV_LOCAL_IP_ADDRESS_HELP "The host TCP/IP address; for example 127.0.0.1.\r\n"

#define SMTPMV_BLOCKED_ADDRESSES "Blocked Addresses"
#define SMTPMV_BLOCKED_ADDRESSES_HELP "The blocked addresses list.\r\n"

#define SMTPMV_ALLOWED_ADDRESSES "Allowed Addresses"
#define SMTPMV_ALLOWED_ADDRESSES_HELP "The allowed addresses list.\r\n"

#define SMTPMV_HOST_ADDRESS "Host Address"
#define SMTPMV_HOST_ADDRESS_HELP "The bracketed host TCP/IP address; for example [127.0.0.1].\r\n"

#define SMTPMV_RELAY_HOST "Relay Host"
#define SMTPMV_RELAY_HOST_HELP "The SMTP server to be used for relaying when delivering messages.\r\n"

#define SMTPMV_DOMAINS_ARRAY "Domains"
#define SMTPMV_DOMAINS_ARRAY_HELP "The agent's configured domain list.\r\n"

#define SMTPMV_USER_DOMAINS_ARRAY "User Domains"
#define SMTPMV_USER_DOMAINS_ARRAY_HELP "The agent's configured user domain list.\r\n"

#define SMTPMV_RELAY_DOMAINS_ARRAY "Relay Domains"
#define SMTPMV_RELAY_DOMAINS_ARRAY_HELP "The agent's configured relay domain list.\r\n"

#define SMTPMV_RBL_HOSTS_ARRAY "RBL Hosts"
#define SMTPMV_RBL_HOSTS_ARRAY_HELP "The agent's configured RBL list.\r\n"

#define SMTPMV_RBL_COMMENTS_ARRAY "RBL Comments"
#define SMTPMV_RBL_COMMENTS_ARRAY_HELP "The agent's configured comments per RBL list entry.\r\n"

#define SMTPMV_USE_RELAY_HOST "Use Relay Host"
#define SMTPMV_USE_RELAY_HOST_HELP "Set to TRUE if the SMTP Relay Host is to be used for remote delivery.\r\n"

#define SMTPMV_ALLOW_EXPN "Allow EXPN"
#define SMTPMV_ALLOW_EXPN_HELP "Set to TRUE to activate support for the SMTP EXPN command; not recommended.\r\n"

#define SMTPMV_ALLOW_VRFY "Allow Verify"
#define SMTPMV_ALLOW_VRFY_HELP "Set to TRUE to activate support for the SMTP VRFY command; not recommended.\r\n"

#define SMTPMV_CHECK_RCPT "Validate Recipient"
#define SMTPMV_CHECK_RCPT_HELP "Set to TRUE to validate recipients addresses during message delivery.\r\n"

#define SMTPMV_SEND_ETRN "Send ETRN"
#define SMTPMV_SEND_ETRN_HELP "Set to TRUE to deliver ETRN messages.\r\n"

#define SMTPMV_ACCEPT_ETRN "Accept ETRN"
#define SMTPMV_ACCEPT_ETRN_HELP "Set to TRUE to accept ETRN messages.\r\n"

#define SMTPMV_BLOCK_RTS_SPAM "Block RTS SPAM"
#define SMTPMV_BLOCK_RTS_SPAM_HELP "Set to TRUE to block recipients in RTS messages if the count exceeds the \"Maximum NULL Send Recipients\" value.\r\n"

#define SMTPMV_BYTE_STORED_COUNT "Remote Messages Delivered"
#define SMTPMV_BYTE_STORED_COUNT_HELP "The number of remote messages delivered.\r\n"

BOOL ManagementInit(const unsigned char *Identity, MDBHandle DirectoryHandle);
void ManagementShutdown(void);
enum ManagementStates ManagementState(void);

BOOL ManagementSetVariables(ManagementVariables *Variables, unsigned long Count);
BOOL ManagementSetCommands(ManagementCommands *Commands, unsigned long Count);

void ManagementServer(void *param);

EXPORT void MsgSendTrap(int type, unsigned char *message);

BOOL ManagementMemoryStats(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
BOOL ManagementConnTrace(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);

#endif    /*    BONGO_MANAGEMENT_H    */
