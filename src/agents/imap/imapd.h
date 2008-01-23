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

#include <xpl.h>
#include <stdio.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <nmlib.h>
#include <bongostore.h>

typedef struct {
    uint32_t uid;
    uint64_t guid;
    unsigned long type;
    unsigned long flags;
    unsigned long size;
    unsigned long internalDate;
    unsigned long headerSize;
    unsigned long bodySize;
} MessageInformation;

#include "store.h"
#include "fetch.h"
#include "rfc822parse.h"

/* Port to listen */
#define IMAP4_PORT  143
#define IMAP4_PORT_SSL 993

#define MAX_FAILED_LOGINS  3


#define STORE_EVENT_LIMIT_EXCEEDED (1 << 0)
#define STORE_EVENT_FLAG           (1 << 1)
#define STORE_EVENT_NEW            (1 << 2)
#define STORE_EVENT_PURGE          (1 << 3)
#define STORE_EVENT_ALL            (STORE_EVENT_FLAG | STORE_EVENT_NEW | STORE_EVENT_PURGE)


/* return values for command functions */
typedef enum {
    STATUS_ABORT = -1,
    STATUS_CONTINUE = 0,
    STATUS_IDENTITY_STORE_FAILURE,
    STATUS_USERNAME_NOT_FOUND,
    STATUS_USERNAME_TOO_LONG,
    STATUS_LINE_TOO_LONG,
    STATUS_PATH_TOO_LONG,
    STATUS_WRONG_PASSWORD,
    STATUS_UNKNOWN_AUTH,
    STATUS_NMAP_COMM_ERROR,
    STATUS_NMAP_CONNECT_ERROR,
    STATUS_NMAP_AUTH_ERROR,
    STATUS_NMAP_PROTOCOL_ERROR,
    STATUS_NOT_MAIL_FOLDER,
    STATUS_FEATURE_DISABLED,
    STATUS_INVALID_FOLDER_NAME,
    STATUS_FOLDER_HAS_INFERIOR,
    STATUS_NO_MATCHING_FOLDER,
    STATUS_NO_SUCH_FOLDER,
    STATUS_FOLDER_ALREADY_EXISTS,
    STATUS_SELECTED_FOLDER_LOST,
    STATUS_READ_ONLY_FOLDER,
    STATUS_COULD_NOT_DETERMINE_QUOTA,
    STATUS_NO_SUCH_QUOTA_ROOT,
    STATUS_MEMORY_ERROR,
    STATUS_PERMISSION_DENIED,
    STATUS_TRY_CREATE,
    STATUS_INVALID_ARGUMENT,
    STATUS_INVALID_STATE,
    STATUS_UNKNOWN_COMMAND,
    STATUS_TLS_NEGOTIATION_FAILURE,
    STATUS_CHARSET_NOT_SUPPORTED,
    STATUS_UID_NOT_FOUND,
    STATUS_NMAPID_NOT_FOUND,
    STATUS_REQUESTED_MESSAGE_NO_LONGER_EXISTS,
    STATUS_IMAPID_NOT_FOUND,
    STATUS_BUG,

    STATUS_MAX
} CommandStatus;

typedef struct {
    long id;
    char *formatString;
} ImapErrorString;

/* STATUS_USERNAME_NOT_FOUND, STATUS_WRONG_PASSWORD, and STATUS_PERMISSION_DENIED */
/* have the same string to prevent spammers from mining the user namespace */

static ImapErrorString ImapErrorStrings[] = {
    { STATUS_IDENTITY_STORE_FAILURE,             "%s NO %s the directory used to lookup users is not functional\r\n" },
    { STATUS_USERNAME_NOT_FOUND,                 "%s NO %s permission denied\r\n" },
    { STATUS_USERNAME_TOO_LONG,                  "%s NO %s username or its alias exceeds system limits\r\n" },
    { STATUS_LINE_TOO_LONG,                      "%s NO %s line too long\r\n" },
    { STATUS_PATH_TOO_LONG,                      "%s NO %s path too long\r\n" },
    { STATUS_WRONG_PASSWORD,                     "%s NO %s permission denied\r\n" },
    { STATUS_UNKNOWN_AUTH,                       "%s NO %s mechanism unknown\r\n" },
    { STATUS_NMAP_COMM_ERROR,                    "%s NO %s unable to communicate with the mail store\r\n" },
    { STATUS_NMAP_CONNECT_ERROR,                 "%s NO %s unable to connect to the mail store\r\n" },
    { STATUS_NMAP_AUTH_ERROR,                    "%s NO %s unable to authenticate to the mail store\r\n" },
    { STATUS_FEATURE_DISABLED,                   "%s NO %s the IMAP service is not enabled for this user\r\n" },
    { STATUS_INVALID_FOLDER_NAME,                "%s NO %s illegal folder name\r\n" },
    { STATUS_FOLDER_HAS_INFERIOR,                "%s NO %s has inferior hieararchical names\r\n" },
    { STATUS_NO_MATCHING_FOLDER,                 "%s NO %s pattern doesn't match mailbox\r\n" },
    { STATUS_SELECTED_FOLDER_LOST,               "%s NO %s selected folder has been lost\r\n" },
    { STATUS_NO_SUCH_FOLDER,                     "%s NO %s no such folder\r\n" },
    { STATUS_FOLDER_ALREADY_EXISTS,              "%s NO %s folder already exists\r\n" },
    { STATUS_READ_ONLY_FOLDER,                   "%s NO %s read only folder!\r\n" },
    { STATUS_COULD_NOT_DETERMINE_QUOTA,          "%s NO %s quota could not be determined\r\n" },
    { STATUS_NO_SUCH_QUOTA_ROOT,                 "%s NO %s no such quota root\r\n" },
    { STATUS_MEMORY_ERROR,                       "%s NO %s out of memory\r\n" },
    { STATUS_PERMISSION_DENIED,                  "%s NO %s permission denied\r\n" },
    { STATUS_TRY_CREATE,                         "%s NO [TRYCREATE] %s\r\n" },
    { STATUS_CHARSET_NOT_SUPPORTED,              "%s NO [BADCHARSET] %s charset not supported\r\n" },
    { STATUS_REQUESTED_MESSAGE_NO_LONGER_EXISTS, "%s NO %s Some of the requested messages no longer exist\r\n" },
    { STATUS_INVALID_ARGUMENT,                   "%s BAD %s invalid arguments\r\n" },
    { STATUS_INVALID_STATE,                      "%s BAD %s invalid command in current state\r\n" },
    { STATUS_UNKNOWN_COMMAND,                    "* BAD command unrecognized\r\n" },
    { STATUS_TLS_NEGOTIATION_FAILURE,            "%s BAD %s negotiation failed\r\n" },
    { 0, NULL }
};

/* supported IMAP commands */

/* IMAP session commands - any state */
#define IMAP_COMMAND_CAPABILITY "CAPABILITY"
#define IMAP_COMMAND_NOOP "NOOP"
#define IMAP_COMMAND_LOGOUT "LOGOUT"

/* IMAP session commands - not authenticated state */
#define IMAP_COMMAND_STARTTLS "STARTTLS"
#define IMAP_COMMAND_AUTHENTICATE "AUTHENTICATE"
#define IMAP_COMMAND_LOGIN "LOGIN"

/* IMAP session commands - authenticated state */
#define IMAP_COMMAND_SELECT "SELECT"
#define IMAP_COMMAND_EXAMINE "EXAMINE"
#define IMAP_COMMAND_CREATE "CREATE"
#define IMAP_COMMAND_DELETE "DELETE"
#define IMAP_COMMAND_RENAME "RENAME"
#define IMAP_COMMAND_SUBSCRIBE "SUBSCRIBE"
#define IMAP_COMMAND_UNSUBSCRIBE "UNSUBSCRIBE"
#define IMAP_COMMAND_LIST "LIST"
#define IMAP_COMMAND_LSUB "LSUB"
#define IMAP_COMMAND_STATUS "STATUS"
#define IMAP_COMMAND_APPEND "APPEND"

/* IMAP session commands - selected state */
#define IMAP_COMMAND_CHECK "CHECK"
#define IMAP_COMMAND_CLOSE "CLOSE"
#define IMAP_COMMAND_EXPUNGE "EXPUNGE"
#define IMAP_COMMAND_SEARCH "SEARCH"
#define IMAP_COMMAND_UID_SEARCH "UID SEARCH"
#define IMAP_COMMAND_FETCH "FETCH"
#define IMAP_COMMAND_UID_FETCH "UID FETCH"
#define IMAP_COMMAND_STORE "STORE"
#define IMAP_COMMAND_UID_STORE "UID STORE"
#define IMAP_COMMAND_COPY "COPY"
#define IMAP_COMMAND_UID_COPY "UID COPY"

/* IMAP ACL commands rfc2086 */
#define IMAP_COMMAND_SETACL "SETACL"
#define IMAP_COMMAND_DELETEACL "DELETEACL"
#define IMAP_COMMAND_GETACL "GETACL"
#define IMAP_COMMAND_LISTRIGHTS "LISTRIGHTS"
#define IMAP_COMMAND_MYRIGHTS "MYRIGHTS"

/* IMAP Quota commands rfc2087 */
#define IMAP_COMMAND_SETQUOTA "SETQUOTA"
#define IMAP_COMMAND_GETQUOTA "GETQUOTA"
#define IMAP_COMMAND_GETQUOTAROOT "GETQUOTAROOT"

/* IMAP Namespace command rfc2342 */
#define IMAP_COMMAND_NAMESPACE "NAMESPACE"

/* non-standard commands */
#define IMAP_COMMAND_PROXYAUTH "PROXYAUTH"  /* depricated - should use SASL */
#define IMAP_HELP_NOT_DEFINED "%s - HELP not defined.\r\n"

/* Internal stuff */
#define CR     0x0d
#define LF     0x0a

/* Command State */
#define STATE_FRESH  (1<<0)
#define STATE_AUTH  (1<<1)
#define STATE_SELECTED (1<<2)
#define STATE_EXITING (1<<3)

#define BUFSIZE    1023

/* Minutes * 60 (1 second granularity) */
#define IMAP_CONNECTION_TIMEOUT (30*60)

#define UID_HIGHEST     ULONG_MAX

#define IMAP_STACK_SIZE  (1024*65)

typedef struct {
    uint64_t guid;                                          /* folder id                        */
    uint32_t uidNext;                                       /* the next expected uid            */
    uint32_t uidRecent;                                     /* the first recent uid             */
    struct {
        char *utf7;                                         /* utf7 encoding of the folder name */
        char *utf8;                                         /* utf8 encoding of the folder name */
    } name;
    BOOL subscribed;                                        /* shown by LSUB                    */
    unsigned long flags;
    //unsigned long acl;                                    /* access rights                    */
} FolderInformation;

typedef struct {
    unsigned char *buffer;
    char *name;
    unsigned long nameLen;
    FolderInformation *object;
} FolderPath;

typedef struct {
    unsigned long uid;
    unsigned long guid;
} NewEvent;

typedef struct {
    unsigned long uid;
    unsigned long sequence;
} PurgeEvent;

typedef struct {
    unsigned long uid;
    unsigned long value;
} FlagEvent;

typedef struct {
    unsigned long remembered;                                /* unaddressed folder events        */
    NewEvent *new;
    unsigned long newCount;
    unsigned long newAllocCount;
    PurgeEvent *purge;
    unsigned long purgeCount;
    unsigned long purgeAllocCount;
    FlagEvent *flag;
    unsigned long flagCount;
    unsigned long flagAllocCount;
    BOOL unseen;
    unsigned long *flags;                               /* unseen message flags             */
} StoreEvents;

typedef struct {
    FolderInformation *info;
    BOOL readOnly;                                          /* folder is read only              */
    StoreEvents events;
    MessageInformation *message;                            /* array of message information     */
    unsigned long messageCount;                             /* number of messages in folder     */
    unsigned long messageAllocated;                         /* number of message structs	*/
    unsigned long recentCount;                              /* number of messages discovered    */
} OpenedFolder;

typedef struct {
    OpenedFolder        selected;                           /* Selected Folder                  */
    unsigned long       count;                              /* number of folders                */
    unsigned long       allocated;                          /* available folder structs         */
    FolderInformation   *list;                              /* array of folder information      */
    uint64_t            rootGuid;                           /* the root mail collection guid    */
} FolderListInformation;

typedef struct {
    struct {
        Connection          *conn;                         /* connection to the store agent    */
        unsigned char       response[1024];                /* used to read store responses    */ 
    } store;

    struct {
        Connection          *conn;                          /* client socket information        */
        int                 state;                          /* IMAP state                       */
        int                 authFailures;                   /* number of authentication failures*/
    } client;

    struct {
        char                *buffer;                        /* buffer for current command       */
        unsigned long       bufferLen;                      /* current length of command buffer */
        char                tag[65];                        /* buffer for current command tag   */
    } command;


    struct {
        char                *name;                          /* login name                       */
    } user;

    FolderListInformation folder;
} ImapSession;

typedef struct {
    struct {
        BongoKeywordIndex *index;
        long *returnValueIndex;

        char *months[12];

        struct {
            int len;
            char message[128];

            struct {
                int len;
                char message[128];
            } ssl;

            struct {
                BOOL enabled;
            } acl;
        } capability;

        struct{
            BongoKeywordIndex *optionIndex;
        } status;

        struct {
            BongoKeywordIndex *typeIndex;
            BongoKeywordIndex *flagIndex;
        } store;
    } command;

    struct {
        struct {
            XplAtomic inUse;
            XplAtomic idle;
            unsigned long max;
        } threads;
        
        XplAtomic served;
        XplAtomic badPassword;
    } session;

    struct {
        struct {
            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        
            unsigned short port;
        } ssl;

        Connection *conn;

        XplAtomic active;

        struct sockaddr_in addr;

        unsigned short port;
        int threadGroupId;
        BOOL disabled;
        XplSemaphore mainSemaphore;
        XplSemaphore shutdownSemaphore;
        char postmaster[MAXEMAILNAMESIZE + 1];
    } server;

    void *logHandle;
    BOOL exiting;
} ImapGlobal;

extern ImapGlobal Imap;

/* IMAP prototypes */

/* event.c */
BOOL EventsCallback(void *param, char *beginPtr, char *endPtr);
long EventsSend(ImapSession *session, unsigned long typesAllowed);
void EventsFree(StoreEvents *events);

/* uid.c */
long UidToSequenceNum(MessageInformation *message, unsigned long messageCount, unsigned long uid, unsigned long *sequenceNum);
long TestUidToSequenceRange(MessageInformation *message, unsigned long messageCount, unsigned long requestUidStart, unsigned long requestUidEnd, long *startNum, long *endNum);
long GetMessageRange(MessageInformation *message, unsigned long messageCount, char **nextRange, unsigned long *rangeStart, unsigned long *rangeEnd, BOOL byUid);

long FolderOpen(Connection *storeConn, OpenedFolder *openFolder, FolderInformation *folder, BOOL readOnly);
long FolderListLoad(ImapSession *session);
long FolderListInitialize(ImapSession *session);
long MessageListLoad(Connection *conn, OpenedFolder *selected);


void CommandFetchCleanup(void);
BOOL CommandFetchInit(void);
void CommandStoreCleanup(void);
BOOL CommandStoreInit(void);
void CommandStatusCleanup(void);
BOOL CommandStatusInit(void);
void CommandSearchCleanup(void);
BOOL CommandSearchInit(void);

/* IMAP command prototypes */
int ImapCommandCapability(void *param);
int ImapCommandNoop(void *param);
int ImapCommandLogout(void *param);
int ImapCommandStartTls(void *param);
int ImapCommandAuthenticate(void *param);
int ImapCommandLogin(void *param);
int ImapCommandSelect(void *param);
int ImapCommandExamine(void *param);
int ImapCommandCreate(void *param);
int ImapCommandDelete(void *param);
int ImapCommandRename(void *param);
int ImapCommandSubscribe(void *param);
int ImapCommandUnsubscribe(void *param);
int ImapCommandList(void *param);
int ImapCommandLsub(void *param);
int ImapCommandStatus(void *param);
int ImapCommandAppend(void *param);
int ImapCommandCheck(void *param);
int ImapCommandClose(void *param);
int ImapCommandExpunge(void *param);
int ImapCommandSearch(void *param);
int ImapCommandUidSearch(void *param);
int ImapCommandFetch(void *param);
int ImapCommandUidFetch(void *param);
int ImapCommandStore(void *param);
int ImapCommandUidStore(void *param);
int ImapCommandCopy(void *param);
int ImapCommandUidCopy(void *param);
int ImapCommandSetAcl(void *param);
int ImapCommandDeleteAcl(void *param);
int ImapCommandGetAcl(void *param);
int ImapCommandListRights(void *param);
int ImapCommandMyRights(void *param);
int ImapCommandSetQuota(void *param);
int ImapCommandGetQuota(void *param);
int ImapCommandGetQuotaRoot(void *param);
int ImapCommandNameSpace(void *param);

#include "inline.h"
