/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005, 2006 Novell, Inc. All Rights Reserved.
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
// Parts Copyright (C) 2007 Alex Hudson. See COPYING for details.

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <assert.h>
#include <limits.h>
#include <time.h>

#include "stored.h"

#include "messages.h"
#include "mime.h"
#include "mail.h"
#include "conversations.h"
#include "calendar.h"
#include "index.h"


int
IsOwnStoreSelected(StoreClient *client)
{
    return (STORE_PRINCIPAL_USER == client->principal.type && 
            (!strcmp(client->principal.name, client->storeName)));
}


void
FindPathToDocFile(StoreClient *client, uint64_t collection,
                  char *dest, size_t size)
{
    snprintf(dest, size, "%s" GUID_FMT ".dat", client->store, collection);
    dest[size-1] = '\0';
}


/* Creates a new collection, including any ancestors required.  Also touches the file
   for this collection (but doesn't truncate it, since this function is also used by
   the RENAME command (which is a bit of an abstraction violation))

   returns:  0 success
            -1 bad collection name
            -2 db lib err
            -3 guid exists
            -4 io error
            -5 permission denied
*/

int
StoreCreateCollection(StoreClient *client, char *name, uint64_t guid,
                      uint64_t *outGuid, int32_t *outVersion)
{
    int dcode = 0;
    DStoreDocInfo info;
    char path[XPL_MAX_PATH + 1];
    char *p;
    FILE *f;
    int namelen = strlen(name);

    /* validate collection name */
    if ('/' != *name ||
        namelen < 2 ||
        namelen > STORE_MAX_COLLECTION - 1 ||
        '/' == name[namelen-1])
    {
        return -1;
    }

    memset (&info, 0, sizeof(DStoreDocInfo));
    info.type = STORE_DOCTYPE_FOLDER;
    info.collection = 1; /* root guid */

    if (guid) {
        switch (DStoreGetDocInfoGuid(client->handle, guid, &info)) {
        case -1:
            return -2;
        case 1:
            return -3;
        default:
            break;
        }
    }

    p = name;
    do {
        info.guid = 0;
        *p++ = '/';
        p = strchr(p, '/');
        if (p) {
            *p = 0;
        } else {
            info.guid = guid;
        }
           
        dcode = DStoreFindCollectionGuid(client->handle, name, &info.collection);
        if (-1 == dcode) {
            return -2;
        } else if (1 == dcode) {
            if (!p) {
                return -3;
            } else {
                continue;
            }
        }

        if (StoreCheckAuthorizationGuidQuiet(client, info.collection, 
                                             STORE_PRIV_BIND)) 
        {
            return -5;
        }

        strncpy (info.filename, name, sizeof(info.filename) - 1);

        if (DStoreSetDocInfo(client->handle, &info)) {
            return -2;
        }
        info.collection = info.guid;

        FindPathToDocFile(client, info.guid, path, sizeof(path));
        f = fopen(path, "ab");
        if (!f) {
            return -4;
        }
        fclose(f);
    } while (p);

    if (outGuid) {
        *outGuid = info.guid;
    }
    if (outVersion) {
        *outVersion = info.version;
    }
    return 0;
}
                 

/* Creates a new document, including any required ancestor collections.
   The new document will have no content.

   returns:  0 success
            -1 bad document name
            -2 db lib err
            -4 io error
            -5 permission denied
*/

int
StoreCreateDocument(StoreClient *client, 
                    StoreDocumentType type, 
                    char *name, 
                    uint64_t *outGuid)
{
    int dcode = 0;
    DStoreDocInfo collinfo;
    DStoreDocInfo info;
    FILE *f;
    char *p;
    char path[XPL_MAX_PATH];

    memset (&info, 0, sizeof(DStoreDocInfo));
    info.type = type;

    if (strlen(name) > STORE_MAX_PATH - 1) {
        return -1;
    }
    strncpy(info.filename, name, sizeof(info.filename));
    p = strrchr(info.filename, '/');
    if (!p) {
        return -1;
    }
    *p = 0;
    
    switch (DStoreGetDocInfoFilename(client->handle, info.filename, &collinfo)) {
    case 0:
        dcode = StoreCreateCollection(client, info.filename, 0, 
                                      &info.collection, NULL);
        if (dcode) {
            return dcode;
        }
        break;
    case 1:
        info.collection = collinfo.guid;
        break;
    default:
        return -1;
    }
    
    *p = '/';
    
    if (DStoreSetDocInfo(client->handle, &info)) {
        return -2;
    }

    FindPathToDocFile(client, info.guid, path, sizeof(path));
    f = fopen(path, "ab");
    if (!f) {
        return -4;
    }
    fclose(f);
    
    *outGuid = info.guid;

    return 0;
}


int 
WriteDocumentHeaders(StoreClient *client, 
                     FILE *fh, 
                     const char *folder,
                     time_t tod)
{
    static char magicbuffer[18] = "0100            \r\n";

    if (sizeof(magicbuffer) != 
        fwrite(magicbuffer, sizeof(unsigned char), sizeof(magicbuffer), fh))
    {
        return -1;
    }

    return sizeof(magicbuffer);
}


static int
CreateDatFile(char *path, int n, size_t len, char *name)
{
    FILE *file;
   
    strncpy(path + n, name, len - n);
    path[XPL_MAX_PATH] = 0;
    
    file = fopen(path, "ab");
    if (!file) {
        return -1;
    }
    fclose(file);

    return 0;
}


/* find the user's store path, creating it if necessary 
   path must be a buffer of size XPL_MAX_PATH + 1 or greater
   returns 0 on success, -1 o/w
*/

int
SetupStore(const char *user, const char **storeRoot, char *path, size_t len)
{
    const char *root;
    int n;
    struct stat sb;

    if (! StoreAgent.installMode) {
        // MsgFindUserStore makes MDB calls and thus can't be used in installmode.
        root = MsgFindUserStore(user, StoreAgent.store.rootDir);
    } else {
        root = StoreAgent.store.rootDir;
    }    
    n = snprintf(path, len, "%s/%s/", root, user);
    path[len] = 0;
    path[n-1] = 0;

    if (stat(path, &sb)) {
        if (XplMakeDir(path)) {
            XplConsolePrintf("Error creating store: %s.\r\n", strerror(errno));
            return -1;
        }
    }
    path[n-1] = '/';

    if (CreateDatFile(path, n, len, "0000000000000002.dat") ||
        CreateDatFile(path, n, len, "0000000000000003.dat") ||
        CreateDatFile(path, n, len, "0000000000000004.dat") ||
        CreateDatFile(path, n, len, "0000000000000005.dat") ||
        CreateDatFile(path, n, len, "0000000000000006.dat") ||
        CreateDatFile(path, n, len, "0000000000000007.dat") ||
        CreateDatFile(path, n, len, "0000000000000008.dat") ||
        CreateDatFile(path, n, len, "0000000000000009.dat") ||
        CreateDatFile(path, n, len, "000000000000000a.dat") ||
        CreateDatFile(path, n, len, "000000000000000b.dat") ||
        CreateDatFile(path, n, len, "000000000000000c.dat") ||
        CreateDatFile(path, n, len, "000000000000000d.dat"))
    {
        XplConsolePrintf("Error creating store: %s.\r\n", strerror(errno));
        return -1;
    }

    path[n] = 0;

    *storeRoot = root;

    return 0;
}


/* This function gets called after a document has been added/replaced to the store,
   before committing the transaction

   NOTE: info->data fields are not valid after this function has been called.
*/

const char *
StoreProcessDocument(StoreClient *client, 
                     DStoreDocInfo *info,
                     DStoreDocInfo *collection,
                     char *path,
                     IndexHandle *indexer,
                     uint64_t linkGuid)
{
    const char *result = NULL;
    DStoreDocInfo conversation;
    int freeindexer = !indexer;
    void *mark = BongoMemStackPeek(&client->memstack);

    /* type-dependent processing */

    switch (info->type) {
    case STORE_DOCTYPE_MAIL:
        result = StoreProcessIncomingMail(client, info, path, &conversation, linkGuid);
        if (result) {
            goto finish;
        }
        collection->imapuid = info->imapuid ? info->imapuid : info->guid;
        if (DStoreSetDocInfo(client->handle, collection)) {
            result = MSG5005DBLIBERR;
            goto finish;
        }
        printf("processed mail with message-id %s\r\n", info->data.mail.messageId);
        break;
    case STORE_DOCTYPE_EVENT:
        result = StoreProcessIncomingEvent(client, info, linkGuid);
        if (result) {
            goto finish;
        }
        break;
    default:
        break;
    }

    /* Index the document */

    if (!indexer) {
        indexer = IndexOpen(client);
        if (!indexer) {
            result = MSG4120INDEXLOCKED;
            goto finish;
        }
    }
    if (IndexDocument(indexer, client, info) != 0) {
        result = MSG5007INDEXLIBERR;
    }
    if (freeindexer) {
        IndexClose(indexer);
    }

finish:
    BongoMemStackPop(&client->memstack, mark);

    return result;
}


static size_t
GenerateFromDelimiter(char *time, char *sender, char *buf, size_t len)
{
    int result;

    if (' ' == time[3]) {
        result = snprintf(buf, len, "From %s %s\r\n", sender, time);        
    } else {
        result = snprintf(buf, len, "From %s %c%c%c%s\r\n", 
                          sender, time[0], time[1], time[2], time + 4);
    }
    buf[len-1] = 0;
    return result;
}


/* FIXME - quota warning (bug 174118) */

/* requires: msgfile open for reading, with file pointer rewound to beginning 
             mboxfile open for writing, with file pointer set to the end of file
             caller is responsible for locking/transactions

   modifies: appends mail record to mboxfile, and leaves that file pointer at EOF
             leaves msgfile pointer at EOF
             writes are not flushed and files are not closed.

   returns: a DELIVERY code
*/

static NMAPDeliveryCode
CopyMessageToMailbox(StoreClient *client,
                     FILE *msgfile, FILE *mboxfile, 
                     char *recipient, 
                     char *sender, char *authSender, char *mbox,
                     unsigned long scmsID, int messageSize, unsigned long flags)
{
    size_t count;
    size_t overhead;
    char timeBuffer[80];
    time_t time_of_day;
    char buffer[CONN_BUFSIZE];
    long startoff;
    long endoff;

    DStoreDocInfo info;

    memset(&info, 0, sizeof(info));

    info.flags = flags;
    info.type = STORE_DOCTYPE_MAIL;

    if (-1 == (startoff = ftell(mboxfile)) || 
        sizeof(info) != fwrite(&info, sizeof(char), sizeof(info), mboxfile))
    {
        goto StoreIOError;
    }

    time(&time_of_day);
    strftime(timeBuffer, 80, "%a %b %d %H:%M %Y", gmtime(&time_of_day));
    overhead = GenerateFromDelimiter(timeBuffer, sender, buffer, sizeof(buffer));
    if (overhead != fwrite(buffer, sizeof(char), overhead, mboxfile)) {
        goto StoreIOError;
    }

    if (authSender && authSender[0] != '-') {
        overhead = sprintf(buffer, "X-Auth-OK: %s\r\n", authSender);
    } else {
        overhead = sprintf(buffer, "X-Auth-No: \r\n");
    }
    if (overhead != fwrite(buffer, sizeof(char), overhead, mboxfile)) {
        goto StoreIOError;
    }

    if (flags) {
        overhead = sprintf(buffer, "X-NIMS-flags: %lu\r\n", flags);
        if (overhead != fwrite(buffer, sizeof(char), overhead, mboxfile)) {
            goto StoreIOError;
        }
    }

    if (mbox) {
        snprintf(info.filename, sizeof(info.filename), "/mail%s%s", (mbox[0] == '/') ? "" : "/", mbox);
        info.filename[sizeof(info.filename)-1] = 0;
        overhead = sprintf(buffer, "X-Mailbox: %s\r\n", mbox);
        if (overhead != fwrite(buffer, sizeof(char), overhead, mboxfile)) {
            goto StoreIOError;
        }
        if (!strcmp(mbox, "/INBOX") || !strcmp(mbox, "INBOX")) {
            info.collection = STORE_MAIL_INBOX_GUID;
        }
    } else {
        strncpy(info.filename, "/mail/INBOX", sizeof(info.filename));
        info.collection = STORE_MAIL_INBOX_GUID;
    }
    
    MsgGetRFC822Date(-1, 0, timeBuffer);
    overhead = fprintf(mboxfile,
                       "Received: from %d.%d.%d.%d [%d.%d.%d.%d] by %s\r\n\twith NMAP (%s); %s\r\n",
                       client->conn->socketAddress.sin_addr.s_net,
                       client->conn->socketAddress.sin_addr.s_host,
                       client->conn->socketAddress.sin_addr.s_lh,
                       client->conn->socketAddress.sin_addr.s_impno,

                       client->conn->socketAddress.sin_addr.s_net,
                       client->conn->socketAddress.sin_addr.s_host,
                       client->conn->socketAddress.sin_addr.s_lh,
                       client->conn->socketAddress.sin_addr.s_impno,

                       StoreAgent.agent.officialName,
                       AGENT_NAME,
                       timeBuffer);
    if (overhead < 0) {
        goto StoreIOError;
    }

    if (scmsID) {
        /* We're using SCMS, only write our minimal header, stop at the body */
        overhead = sprintf(buffer, "X-SCMS-ID: %lu\r\n", scmsID);
        if (overhead != fwrite(buffer, sizeof(char), overhead, mboxfile)) {
            goto StoreIOError;
        }

        overhead = sprintf(buffer, "\r\nThis message is stored in SCMS\r\n");
        if (overhead != fwrite(buffer, sizeof(char), overhead, mboxfile)) {
            goto StoreIOError;
        }
    } else {
        overhead = sprintf(buffer, "Return-Path: <%s>\r\n", sender);
        if (overhead != fwrite(buffer, sizeof( char), overhead, mboxfile)) {
            goto StoreIOError;
        }

        while (!feof(msgfile)) {
            count = fread(buffer, sizeof(char), sizeof(buffer), msgfile);
            if (count > 0) {
                if (count != fwrite(buffer, sizeof(char), count, mboxfile)) {
                    goto StoreIOError;
                }
            } else if (ferror(msgfile)) {
                goto StoreIOError;
            }
        }
        
        //XplSafeAdd(NMAP.stats.storedLocal.bytes, (messageSize + 1023) / 1024);
        //XplSafeIncrement(NMAP.stats.storedLocal.count);
    }

    if (2 != fwrite("\r\n", sizeof(unsigned char), 2, mboxfile)) {
        goto StoreIOError;
    }

    if (-1 == (endoff = ftell(mboxfile)) || 
        fseek(mboxfile, startoff, SEEK_SET))
    {
        goto StoreIOError;
    }

    info.bodylen = endoff - startoff - sizeof(info);
    if (sizeof(info) != fwrite(&info, sizeof(char), sizeof(info), mboxfile) ||
        fseek(mboxfile, 0, SEEK_END))
    {
        goto StoreIOError;
    }

    return DELIVER_SUCCESS;

StoreIOError:

    XplConsolePrintf ("Couldn't deliver to incoming file: %s\r\n", strerror(errno));
    if (ENOSPC == errno) {
        return DELIVER_QUOTA_EXCEEDED;
    }
    return DELIVER_TRY_LATER;
}


/* returns: 0  success
            -1 failure, store is intact
            -2 failure, and journal check is required 
*/

int
StoreCompactCollection(StoreClient *client, uint64_t collection)
{
    FILE *fh = NULL;
    FILE *newfh = NULL;
    int newfd = -1;
    int trans = 0;
    char path[XPL_MAX_PATH+1];
    char newpath[XPL_MAX_PATH+1];
    char savepath[XPL_MAX_PATH+1];
    char *p;
    NLockStruct *lock;
    DStoreStmt *stmt = NULL;
    int result = -1;
    
    newpath[0] = 0;

    printf("Compacting collection " GUID_FMT "\r\n", collection);

    if (StoreGetExclusiveLockQuiet(client, &lock, collection, 2000)) {
        return -1;
    }

    FindPathToDocFile(client, collection, path, sizeof(path));
    fh = fopen(path, "rb");
    if (!fh) {
        goto finish;
    }

    strncpy(newpath, path, sizeof(newpath));
    p = strrchr(newpath, '/');
    strcpy(p + 1, "tmp.XXXXXX");
    newfd = mkstemp(newpath);
    if (-1 == newfd) {
        goto finish;
    }
    newfh = fdopen(newfd, "wb");
    if (!newfh) {
        close (newfd);
        goto finish;
    }

    /* save the current .dat file a as a backup: */

    strncpy(savepath, path, sizeof(savepath));
    p = strrchr(savepath, '.');
    if (!p || strcmp(".dat", p)) {
        goto finish;
    }
    strcpy(p, ".bak");
    
    if (DStoreAddJournalEntry(client->handle, collection, savepath)) {
        goto finish;
    }
    
    if (rename(path, savepath)) {
        DStoreRemoveJournalEntry(client->handle, collection);
        goto finish;
    }

    /* the store is in an unusable state now (temporarily, we hope) */
    result = -2;

    /* write the compacted version of the .dat file into a temp file: */

    if (DStoreBeginTransaction(client->handle)) {
        goto finish;
    }
    trans = 1;
        
    if (DStoreResetTempDocsTable(client->handle)) {
        goto finish;
    }

    stmt = DStoreListColl(client->handle, collection, -1, -1);
    if (!stmt) {
        goto finish;
    }

    while (1) {
        DStoreDocInfo info;
        int dcode;
        size_t total;
        size_t count;
        char buffer[CONN_BUFSIZE];

        dcode = DStoreInfoStmtStep(client->handle, stmt, &info);
        if (0 == dcode) {
            break;
        } else if (-1 == dcode) {
            goto finish;
        }
        
        if (STORE_IS_FOLDER(info.type)) {
            continue;
        }

        if (XplFSeek64(fh, info.start + info.headerlen, SEEK_SET)) {
            goto finish;
        }
        info.start = ftell(newfh);

        info.headerlen = WriteDocumentHeaders(client, newfh, NULL, 
                                              info.timeCreatedUTC);
        if (info.headerlen < 0) {
            goto finish;
        }
        
        for (total = info.bodylen;
             total;
             total -= count)
        {
            count = sizeof(buffer) < total ? sizeof(buffer) : total;

            count = fread(buffer, sizeof(char), count, fh);
            if (!count ||
                ferror(fh) || 
                count != fwrite(buffer, sizeof(char), count, newfh))
            {
                goto finish;
            }
        }
        
        if (DStoreAddTempDocsInfo(client->handle, &info)) {
            goto finish;
        }
    }

    if (DStoreMergeTempDocsTable(client->handle)) {
        goto finish;
    }

    if (fclose(newfh)) {
        goto finish;
    }
    newfh = NULL;

    if (fclose(fh)) {
        goto finish;
    }
    fh = NULL;

    /* now move the compacted file into the correct place */

    if (DStoreRemoveJournalEntry(client->handle, collection)) {
        goto finish;
    }

    if (rename(newpath, path)) {
        goto finish;
    }
    newpath[0] = 0;

    if (DStoreCommitTransaction(client->handle)) {
        goto finish;
    }
    trans = 0;

    unlink(savepath);

    /* success */
    result = 0;

finish:
    if (stmt) {
        DStoreStmtEnd(client->handle, stmt);
    }

    if (trans) {
        DStoreAbortTransaction(client->handle);
    }

    if (newfh) {
        fclose(newfh);
    }
    if (fh) {
        fclose(fh);
    }

    if (-2 == result) {
        if (rename(savepath, path)) { 
            /* Something has gone wrong, and the collection is now in an inconsistent 
               state, despite our efforts to restore the backup file.  To prevent
               corrupted access, we are going to return without releasing our lock 
               on the collection.  FIXME - do something more graceful...
            */
            printf("Collection " GUID_FMT " requires maintenance "
                   "after failed compaction.\r\n", collection);
            
            return -2;
        } else {
            /* Compaction failed, but at least we've returned to the original 
               state (except we're going to leave a harmless journal entry) */
            result = -1;
        }
    }

    if (newpath[0]) {
        unlink(newpath);
    }

    StoreReleaseExclusiveLock(client, lock);
    StoreReleaseSharedLock(client, lock);

    return result;
}


static void
CompactAllCollections(StoreClient *client)
{
    DIR *dir;
    struct dirent *dirent;

    dir = opendir(client->store);
    if (!dir) {
        return;
    }

    while ((dirent = readdir(dir))) {
        uint64_t guid;
        char *endp;

        guid = HexToUInt64(dirent->d_name, &endp);
        if (endp != dirent->d_name + 16 || strcmp(".dat", endp)) {
            continue;
        }
        
        StoreCompactCollection(client, guid);
    }

    closedir(dir);
}


/* deliver message to a mailbox 
   msgfile must be an open file whose file pointer set to the start of the file.

   Implementation: This function actually delivers mail to an "incoming" file, 
   which is read upon store selection.  
*/

/* FIXME: If something goes wrong while writing to the incoming file, the store
   won't be able to recover (bug 175411) */

NMAPDeliveryCode
DeliverMessageToMailbox(StoreClient *client,
                        char *sender, char *authSender, char *recipient, char *mbox,
                        FILE *msgfile, unsigned long scmsID, size_t msglen, 
                        unsigned long flags)
{
    int result = DELIVER_FAILURE;
    int fd;
    FILE *mboxfile = NULL;
    NLockStruct *lock;
    const char *storeRoot;
    char path[XPL_MAX_PATH+1];
    ssize_t inclen = 0;

    lock = NULL;

    if (SetupStore(recipient, &storeRoot, path, sizeof(path))) {
        return DELIVER_TRY_LATER;
    }

    if (StoreGetExclusiveLockQuiet(client, &lock, STORE_MAIL_GUID, 2000)) {
        return DELIVER_TRY_LATER;
    }

    strcat (path, "incoming");

    /* I want read/write/create/!truncate - don't think it can be done with fopen? */
    if (-1 == (fd = open(path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)) ||
        NULL == (mboxfile = fdopen(fd, "r+b")))
    {
        result = ENOSPC == errno ? DELIVER_QUOTA_EXCEEDED : DELIVER_FAILURE;
        goto finish;
    }
    if (fseek (mboxfile, 0L, SEEK_END)) {
        result = DELIVER_FAILURE;
        goto finish;
    }

    /* XplConsolePrintf("OFFSET: %ld\r\n", ftell(mboxfile)); */

    result = CopyMessageToMailbox (client, msgfile, mboxfile, recipient, 
                                   sender, authSender, mbox, 
                                   scmsID, msglen, flags);

    inclen = ftell(mboxfile);
    if (-1 == inclen) {
        result = DELIVER_FAILURE;
    }

finish:

    if (mboxfile && fclose(mboxfile)) {
        result = ENOSPC == errno ? DELIVER_QUOTA_EXCEEDED : DELIVER_FAILURE;
    }

    StoreReleaseExclusiveLock(client, lock);
    StoreReleaseSharedLock(client, lock);

    if (DELIVER_SUCCESS == result && 
        inclen > StoreAgent.store.incomingQueueBytes) 
    {
        /* this store has a bit of incoming mail, let's try to import it now.
           FIXME: it'd probably be better to pass the "incoming" lock through
        */

        SelectStore(client, recipient);
        UnselectStore(client);
    }

    return result;
}



/* modifies: imports mail from the "incoming" file into the store.  Upon success,
             the incoming file is truncated.  

   returns:   1+ - success, number of imported documents
              0  - retry (recoverable failure due to locked file, etc.)
              -1 - failure (corrupt file, or incompatible version)
*/

int
ImportIncomingMail(StoreClient *client)
{
    int result = 0;
    DStoreDocInfo info;
    char incomingPath[XPL_MAX_PATH+1];
    FILE *msgfile = NULL;
    int trans = 0;
    NLockStruct *incLock = NULL; /* lock of "incoming" file */
    struct stat sb;
    IndexHandle *index = NULL;
    long offset = 0;
    CollectionLockPool collLocks;
    WatchEventList events;
    
    if (CollectionLockPoolInit(client, &collLocks)) {
        return 0;
    }
    if (WatchEventListInit(&events)) {
        CollectionLockPoolDestroy(&collLocks);
        return 0;
    }

    if (StoreGetExclusiveLockQuiet(client, &incLock, STORE_MAIL_GUID, 2000)) {
        CollectionLockPoolDestroy(&collLocks);
        WatchEventListDestroy(&events);
        return 0;
    }

    snprintf(incomingPath, sizeof(incomingPath), "%sincoming", client->store);
    incomingPath[sizeof(incomingPath)-1] = 0;

    if (-1 == stat(incomingPath, &sb)) {
        if (ENOENT != errno) {
            result = -1;
        }
        goto cleanup;
    }

    if (0 == sb.st_size) {
        goto cleanup;
    }

    msgfile = fopen(incomingPath, "r");

    if (!msgfile) {
        result = ENOENT == errno ? 0 : -1;
        goto cleanup;
    }
    
    index = IndexOpen(client);
    if (!index) {
        result = 0;
        goto cleanup;
    }

    if (DStoreBeginTransaction(client->handle)) {
        goto cleanup;
    }
    trans = 1;

    /* Read through the incoming file, and import messages one by one.
       If something goes wrong, jump to "finish" if the already
       imported mail is okay, and to "cleanup" to abort the entire
       import.
    */

    for (offset = 0;
         sizeof(info) == fread(&info, sizeof(char), sizeof(info), msgfile);
         offset = ftell(msgfile))
    {
        int dcode = 0;
        DStoreDocInfo collinfo;
        NLockStruct *collLock;
        char path[XPL_MAX_PATH+1];
        FILE *destfile = NULL;
        char buffer[CONN_BUFSIZE];
        size_t count;
        unsigned long len;


        if (-1 == offset) {
            result = -1;
            goto cleanup;
        }            

        if (!info.collection) {
            dcode = DStoreFindCollectionGuid(client->handle, info.filename,
                                         &info.collection);
            if (0 == dcode) { 
                /* delivery to non-existing collection; try to create it */
                if (StoreCreateCollection(client, info.filename, 0, 
                                          &info.collection, NULL)) 
                {
                    /* fallback to /mail/INBOX */
                    info.collection = STORE_MAIL_INBOX_GUID;
                } 
            } else if (-1 == dcode) {
                printf ("couldn't write to collection %s due to db error.\n", 
                        info.filename);
                goto finish;
            }
        }

        if (1 != DStoreGetDocInfoGuid(client->handle, info.collection, &collinfo)) {
            goto finish;
        }
        snprintf(info.filename, sizeof(info.filename), "%s/", collinfo.filename);

        collLock = CollectionLockPoolGet(&collLocks, collinfo.guid);
        if (!collLock) {
            goto finish;
        }

        FindPathToDocFile(client, info.collection, path, sizeof(path));

        destfile = fopen(path, "ab");
        if (!destfile) {
            goto finish;
        }

        info.start = ftell(destfile);
        if (-1 == info.start) {
            goto finish;
        }

        info.timeCreatedUTC = (uint64_t) time(NULL);

        info.headerlen = WriteDocumentHeaders(client, destfile, NULL,
                                              info.timeCreatedUTC);
        if (info.headerlen < 0) {
            goto finish;
        }
        
        for (len = info.bodylen; len; len -= count) {
            count = len < sizeof(buffer) ? len : sizeof(buffer);
            count = fread(buffer, sizeof(char), count, msgfile);
            if (!count) {
                result = -1;
                goto cleanup;
            }
            if (count != fwrite(buffer, sizeof(char), count, destfile)) {
                goto finish;
            }
        }
        
        if (fclose(destfile)) {
            goto finish;
        }
        
        if (DStoreSetDocInfo(client->handle, &info)) {
            goto finish;
        }

        if (StoreProcessDocument(client, &info, &collinfo, path, index, 0)) {
            /* we were unable to process this message for some reason, so
               let's save it to the side and skip over it */

            char failedPath[XPL_MAX_PATH+1];
            FILE *failfile;
            long newoffset = ftell(msgfile);

            if (DStoreDeleteDocInfo(client->handle, info.guid)) {
                goto cleanup;
            }

            snprintf(failedPath, sizeof(failedPath), "%sfailed", client->store);
            failedPath[sizeof(failedPath)-1] = 0;

            failfile = fopen(failedPath, "ab");
            if (!failfile) {
                goto finish;
            }

            if (fseek(msgfile, offset, SEEK_SET) || 
                BongoFileCopyBytes(msgfile, failfile, sizeof(info) + info.bodylen) ||
                fseek(msgfile, newoffset, SEEK_SET))
            {
                fclose(failfile);
                goto finish;
            }

            if (fclose(failfile)) {
                goto finish;
            }

            printf("Unparsable message saved in %s\n", failedPath);
        } else {
            /* success! */
            collinfo.imapuid = info.guid;
            if (DStoreSetDocInfo(client->handle, &collinfo)) {
                result = -1;
                goto cleanup;
            }
            ++result;

            if (WatchEventListAdd(&events, collLock, STORE_WATCH_EVENT_NEW, 
                                  info.guid, info.guid, 0))
            {
                result = -1;
                goto cleanup;
            }
        }
    }

finish:
    IndexClose(index);
    index = NULL;

    if (fseek(msgfile, offset, SEEK_SET)) {
        result = 0;
        goto cleanup;
    }

    if (DStoreCommitTransaction(client->handle)) {
        result = 0;
        goto cleanup;
    }
    trans = 0;

    /* FIXME: In the (hopefully unlikely) event that something goes wrong
       after the transaction is commited, but before the incoming file
       is truncated, then duplicate documents will result from the
       next time this function is called.  This could be fixed by using
       a journal (see StoreCompactCollections).  (bug 175413)
    */

    client->stats.insertions += result;

    if (feof(msgfile)) {
        truncate(incomingPath, 0);
    } else {
        char tmppath[XPL_MAX_PATH+1];
        char *p;
        int tmpfd;
        FILE *tmpfile;

        strncpy(tmppath, incomingPath, sizeof(tmppath));
        p = strrchr(tmppath, '/');
        strncpy(p + 1, "i.XXXXXX", strlen("incoming")); 
        tmpfd = mkstemp(tmppath);
        if (-1 == tmpfd) {
            goto cleanup;
        }
        tmpfile = fdopen(tmpfd, "wb");
        if (!tmpfile) {
            close (tmpfd);
            unlink(tmppath);
            goto cleanup;
        }

        if (BongoFileCopyBytes(msgfile, tmpfile, 0)) {
            fclose(tmpfile);
            unlink(tmppath);
            goto cleanup;
        }
        fclose(tmpfile);
        fclose(msgfile);
        msgfile = NULL;

        if (rename(tmppath, incomingPath)) {
            /* unlink(tmppath); */
        }

        WatchEventListFire(&events, NULL);
    }

cleanup:
    if (msgfile) {
        fclose(msgfile);
    }

    if (trans) {
        client->flags |= STORE_CLIENT_FLAG_NEEDS_COMPACTING;
        DStoreAbortTransaction(client->handle);
    }

    if (index) {
        IndexClose(index);
    }
    
    CollectionLockPoolDestroy(&collLocks);
    WatchEventListDestroy(&events);

    StoreReleaseExclusiveLock(client, incLock);
    StoreReleaseSharedLock(client, incLock);

    if (result < 0) {
        printf("Unable to import mail for store %s: result %d\r\n",
               client->store, result);
    }

    return result;
}

/* login */
CCode
SelectUser(StoreClient *client, char *user, char *password, int nouser)
{
    MDBValueStruct *vs = NULL;
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    CCode ccode = -1;
    struct sockaddr_in serv;
    char buf[INET_ADDRSTRLEN+1];

    if (StoreAgent.installMode) {
        // Don't let users login in install mode.
        // FIXME: a better error message? 
        ccode = ConnWriteStr(client->conn, MSG4224NOUSER);
        goto finish;
    }

    vs = MDBCreateValueStruct(StoreAgent.handle.directory, NULL);
    if (!vs) {
        return ConnWriteStr(client->conn, MSG5001NOMEMORY);
    }

    if (!MsgFindObject(user, dn, NULL, &serv, vs)) {
        XplConsolePrintf("Couldn't find user object for %s\r\n", user);
        if (IS_MANAGER(client)) {
            ccode = ConnWriteStr(client->conn, MSG4224NOUSER);
        } else {
            ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
            XplDelay(2000);
        }
        goto finish;
    }

    if (password && !MDBVerifyPassword(dn, password, vs)) {
        ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
        XplDelay(2000);
        goto finish;
    }

    if (NULL == inet_ntop(serv.sin_family, &serv.sin_addr, buf, sizeof(buf))) {
        ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
        goto finish;
    }

success:
    ccode = ConnWriteF(client->conn, "1000 %s\r\n", buf);

    if (nouser) {
        goto finish;
    }

    UnselectUser(client);

    client->principal.type = STORE_PRINCIPAL_USER;
    strncpy(client->principal.name, user, sizeof(client->principal.name));
    client->principal.name[sizeof(client->principal.name) - 1] = 0;
    client->flags |= STORE_CLIENT_FLAG_IDENTITY;

finish:
    if (vs) {
        MDBDestroyValueStruct(vs);
    }
    return ccode;
}


static int
CheckJournal(DStoreHandle *handle)
{
    DStoreStmt *stmt;
    uint64_t collection;
    const char *filename;
    int dcode;
    int result = -1;
    struct stat sb;
    char *p;
    char path[XPL_MAX_PATH + 1];

    if (DStoreBeginTransaction(handle)) {
        return -1;
    }

    while (1) {
        stmt = DStoreListJournal(handle);
        if (!stmt) {
            goto finish;
        }
        
        dcode = DStoreJournalStmtStep(handle, stmt, &collection, &filename);
        switch (dcode) {
        case 0:
            if (!DStoreCommitTransaction(handle)) {
                result = 0;
            }
            goto finish;
        case -1:
        default:
            goto finish;
        case 1:
            if (stat(filename, &sb)) {
                if (ENOENT != errno) {
                    goto finish;
                }
            } else {
                strncpy(path, filename, sizeof(path));
                p = strrchr(path, '.');
                if (!p || strcmp(".bak", p)) {
                    printf ("Don't know how to restore backup file %s\n", filename);
                    goto finish;
                }
                strcpy(p, ".dat");
                
                if (rename(filename, path)) {
                    printf ("Couldn't restore backup file %s to %s\n", 
                            filename, path);
                    goto finish;
                }
            }
 
            /* a bit awkward: we have to close the journal table cursor 
               in order to delete the current entry */
            DStoreStmtEnd(handle, stmt);
            stmt = NULL;
            
            if (DStoreRemoveJournalEntry(handle, collection)) {
                printf ("Couldn't update journal after restoring file %s\n", 
                        filename);
                goto finish;
            }
        }
    }
    
finish:
    if (stmt) {
        DStoreStmtEnd(handle, stmt);
    }

    if (result) {
        DStoreAbortTransaction(handle);
    }

    return result;
}


/* opens the store for the given user */
/* returns: -1 on error */

int 
SelectStore(StoreClient *client, char *user)
{
    const char *storeRoot;
    char path[XPL_MAX_PATH + 1];
    DStoreHandle *dbhandle;
    
    if (client->storeName && !strcmp(user, client->storeName)) {
        return 0;
    }

    if (SetupStore(user, &storeRoot, path, sizeof(path))) {
        return -1;
    }

    dbhandle = DBPoolGet(user);
    if (dbhandle) {
        DStoreSetMemStack(dbhandle, &client->memstack);
    } else {
        dbhandle = DStoreOpen(path, &client->memstack, client->lockTimeoutMs);
        if (!dbhandle) {
            return -1;
        }
    }

    if (CheckJournal(dbhandle)) {
        DStoreClose(dbhandle);
        return -1;
    }

    /* close the old store */
    UnselectStore(client);

    strcpy(client->store, path);
    client->storeRoot = storeRoot;
    client->storeHash = BongoStringHash(user);
    client->storeName = MemStrdup(user);
    client->handle = dbhandle;

    client->stats.insertions = 0;
    client->stats.updates = 0;
    client->stats.deletions = 0;

    ImportIncomingMail(client);

    return 0;
}


/* closes the current store, if necessary */
void
UnselectStore(StoreClient *client)
{
    if (client->flags & STORE_CLIENT_FLAG_NEEDS_COMPACTING) {
        CompactAllCollections(client);
        client->flags &= ~STORE_CLIENT_FLAG_NEEDS_COMPACTING;
    }

/*
    printf ("Stats: %d insertions, %d updates, %d deletions\n",
            client->stats.insertions, client->stats.updates, client->stats.deletions);
*/

    if (client->storeName && 
        client->stats.insertions + client->stats.updates + client->stats.deletions >
        (rand() % 50))
    {
        IndexHandle *index;

        index = IndexOpen(client);
        if (index) {
            printf("Optimizing index for store %s\n", client->storeName);
            IndexOptimize(index);
            IndexClose(index);
        }
    }

    if (client->handle) {
        if ((client->flags & STORE_CLIENT_FLAG_DONT_CACHE) || 
            DBPoolPut(client->storeName, client->handle)) 
        {
            DStoreClose(client->handle);
        }
        client->handle = NULL;
        client->flags &= ~STORE_CLIENT_FLAG_DONT_CACHE;
    }

    if (client->watch.collection) {
        NLockStruct *lock;

        while (StoreGetExclusiveLockQuiet(client, &lock, client->watch.collection, 
                                          600000))
        { } /* block b/c we need this lock no matter what to free the client */
        
        if (StoreWatcherRemove(client, lock)) {
            printf("Internal error removing client watch");
        }

        StoreReleaseExclusiveLock(client, lock);
        StoreReleaseSharedLock(client, lock);
    }

    if (client->storeName) {
        MemFree(client->storeName);
        client->storeName = NULL;
    }
}


void
UnselectUser(StoreClient *client)
{
    StoreToken *tok;
    StoreToken *next;

    for (tok = client->principal.tokens; 
         tok; 
         tok = next) 
    {
        next = tok->next;
        MemFree(tok);
    }
    client->principal.tokens = NULL;
    client->principal.type = STORE_PRINCIPAL_NONE;
    client->flags &= ~STORE_CLIENT_FLAG_IDENTITY;
}


static int
RemoveCurrentDirectory()
{
    DIR *dir;
    struct dirent *dirent;
    struct stat sb;
    int result = -1;

    dir = opendir(".");
    if (!dir) {
        return -1;
    }

    while ((dirent = readdir(dir))) {
        if (!strcmp(".", dirent->d_name) ||
            !strcmp("..", dirent->d_name))
        {
            continue;
        }

        if (stat(dirent->d_name, &sb)) {
            goto finish;
        }
        
        if (S_ISREG(sb.st_mode)) {
            unlink(dirent->d_name);
        } else if (S_ISDIR(sb.st_mode)) {
            if (chdir(dirent->d_name) ||
                RemoveCurrentDirectory() ||
                chdir(".."))
            {
                goto finish;
            }                             
        }
    }
    result = 0;

finish:
    closedir(dir);

    return result;
}


void
DeleteStore(StoreClient *client)
{
    char path[XPL_MAX_PATH];
    int cwd;

    strncpy(path, client->store, sizeof(path));
    cwd = open(".", O_RDONLY);
    if (cwd < 0) {
        return;
    }
    
    client->flags |= STORE_CLIENT_FLAG_DONT_CACHE;

    UnselectStore(client);
    
    if (chdir(path)) {
        return;
    }
    RemoveCurrentDirectory();
    fchdir(cwd);
}


/* */
/* returns: -1 on error */
int
SetParentCollectionIMAPUID(StoreClient *client, 
                           DStoreDocInfo *parent,
                           DStoreDocInfo *doc)
{
    DStoreDocInfo info;

    if (!parent) {
        switch (DStoreGetDocInfoGuid(client->handle, doc->collection, &info)) {
        case 0:
            return -1;
        case 1:
            parent = &info;
            break;
        case -1:
            return -1;
        }
    }
    parent->imapuid = doc->guid + 1;
    return DStoreSetDocInfo(client->handle, parent);
}

BongoJsonResult
GetJson(StoreClient *client, DStoreDocInfo *info, BongoJsonNode **node)
{
    BongoJsonResult ret = BONGO_JSON_UNKNOWN_ERROR;
    char path[XPL_MAX_PATH + 1];
    FILE *fh;
    char *buf;
    
    FindPathToDocFile(client, info->collection, path, sizeof(path));

    fh = fopen(path, "rb");
    if (!fh) {
        printf("bongostore: Couldn't open file for doc " GUID_FMT "\n", info->guid);
        goto finish;
    }
    if (0 != XplFSeek64(fh, info->start + info->headerlen, SEEK_SET)) {
        printf("bongostore: Couldn't seek to doc " GUID_FMT "\n", info->guid);
        goto finish;
    }


    /* FIXME: would be nice (and easy) to get a streaming API for
     * bongojson */ 
    buf = MemMalloc(info->bodylen + 1);
    
    if (fread(buf, 1, info->bodylen, fh) == info->bodylen) {
        buf[info->bodylen] = '\0';        
    } else {
        printf("bongostore: Couldn't read doc " GUID_FMT "\n", info->guid);
        goto finish;
    }
    
    ret = BongoJsonParseString(buf, node);

    if (ret != BONGO_JSON_OK) {
        printf("bongostore: Couldn't parse json object %s\n", buf);
    }   
    
finish :
    fclose (fh);

    return ret;    
}


/** lock convenience functions **/


int
StoreGetExclusiveLockQuiet(StoreClient *client, NLockStruct **lock, 
                           uint64_t doc, time_t timeout)
{
    *lock = NULL;
    if (NLOCK_ACQUIRED != ReadNLockAcquire(lock, &client->storeHash, client->store, 
                                           doc, timeout / 2))
    {
        return -1;
    }    
    if (NLOCK_ACQUIRED != PurgeNLockAcquire(*lock, timeout / 2)) {
        ReadNLockRelease(*lock);
        return -1;
    }
    return 0;
}
             

CCode
StoreGetExclusiveLock(StoreClient *client, NLockStruct **lock,
                      uint64_t doc, time_t timeout)
{
    if (StoreGetExclusiveLockQuiet(client, lock, doc, timeout)) {
        return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
    }
    return 0;
}


CCode
StoreGetSharedLockQuiet(StoreClient *client, NLockStruct **lock,
                        uint64_t doc, time_t timeout)
{
    *lock = NULL;
    if (NLOCK_ACQUIRED != ReadNLockAcquire(lock, &client->storeHash, client->store, 
                                           doc, timeout))
    {
        return -1;
    }
    return 0;
}


CCode
StoreGetSharedLock(StoreClient *client, NLockStruct **lock,
                   uint64_t doc, time_t timeout)
{
    if (StoreGetSharedLockQuiet(client, lock, doc, timeout)) {
        return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
    }
    return 0;
}


/* gets an exclusive lock on the collection.  If client->watchLock is not NULL,
   it is released.
 */

CCode 
StoreGetCollectionLock(StoreClient *client, NLockStruct **lock, uint64_t coll)
{
    if (StoreGetSharedLockQuiet(client, lock, coll, 3000)) {
        return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
    }
    
    if (*lock == client->watchLock) {
        StoreReleaseSharedLock(client, client->watchLock);
        client->watchLock = NULL;
    }

    if (NLOCK_ACQUIRED != PurgeNLockAcquire(*lock, 3000)) {
        ReadNLockRelease(*lock);
        return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
    }
    
    return 0;
}


/** Pooled collection locks **/

typedef struct {
    uint64_t guid;
    NLockStruct *lock;
} colldatum;


int 
CollectionLockPoolInit(StoreClient *client, CollectionLockPool *pool)
{
    pool->client = client;
    return BongoArrayInit(&pool->colls, sizeof(colldatum), 8);
}


void
CollectionLockPoolDestroy(CollectionLockPool *pool)
{
    unsigned int i;

    for (i = 0; i < pool->colls.len; i++) {
        colldatum coll = BongoArrayIndex(&pool->colls, colldatum, i);

        StoreReleaseExclusiveLock(pool->client, coll.lock);
        StoreReleaseSharedLock(pool->client, coll.lock);
    }

    BongoArrayDestroy(&pool->colls, TRUE);
}


NLockStruct *
CollectionLockPoolGet(CollectionLockPool *pool, uint64_t guid)
{
    colldatum coll;
    unsigned int i;
    
    for (i = 0; i < pool->colls.len; i++) {
        colldatum coll = BongoArrayIndex(&pool->colls, colldatum, i);
        
        if (guid == coll.guid) {
            return coll.lock;
        }
    }

    if (StoreGetExclusiveLockQuiet(pool->client, &coll.lock, guid, 5000)) {
        return NULL;
    }
    coll.guid = guid;
    BongoArrayAppendValue(&pool->colls, coll);
    return coll.lock;
}


/** Watch stuff **/


/* adds the client as a watcher of the collection locked by cLock 
   returns: -1 on failure, 0 o/w
*/

int
StoreWatcherAdd(StoreClient *client,
                NLockStruct *cLock)
{
    int i;

    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (!cLock->watchers[i]) {
            cLock->watchers[i] = client;
            return 0;
        }
    }
    return -1;
}


int
StoreWatcherRemove(StoreClient *client,
                   NLockStruct *cLock)
{
    int i;

    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (client == cLock->watchers[i]) {
            cLock->watchers[i] = NULL;
            client->watch.collection = 0;
            return 0;
        }
    }
    return -1;
}


void
StoreWatcherEvent(StoreClient *thisClient,  /* can be NULL */
                  NLockStruct *cLock,
                  StoreWatchEvents event,
                  uint64_t guid,
                  uint32_t imapuid,
                  uint32_t flags)
{
    int i;
    StoreClient *client;

    if (!imapuid) {
        imapuid = guid;
    }
                  
    for (i = 0; i < STORE_COLLECTION_MAX_WATCHERS; i++) {
        if (cLock->watchers[i]) {
            client = cLock->watchers[i];

            if (client != thisClient) {
                if (!(client->watch.flags & event)) {
                    continue;
                }
                if (client->watch.journal.count < STORE_CLIENT_WATCH_JOURNAL_LEN) {
                    client->watch.journal.events[client->watch.journal.count] = event;
                    client->watch.journal.guids[client->watch.journal.count] = guid;
                    client->watch.journal.imapuids[client->watch.journal.count] = imapuid;
                    client->watch.journal.flags[client->watch.journal.count] = flags;
                }
                ++client->watch.journal.count;
            }
        }
    }
}


CCode
StoreShowWatcherEvents(StoreClient *client)
{
    CCode ccode = 0;
    int i;

    ccode = StoreGetExclusiveLock(client, &client->watchLock, 
                                  client->watch.collection, 6000);
    if (ccode) { 
        return ccode;
    }

    if (client->watch.journal.count > STORE_CLIENT_WATCH_JOURNAL_LEN) {
        ccode = ConnWriteStr(client->conn, MSG6000RESET);
    } else {
        for (i = 0; i < client->watch.journal.count && ccode >= 0; i++) {
            int event = client->watch.journal.events[i];
            if (STORE_WATCH_EVENT_FLAGS == event) {
                ccode = ConnWriteF(client->conn, 
                                   "6000 FLAGS " GUID_FMT " %08x %d\r\n",
                                   client->watch.journal.guids[i],
                                   client->watch.journal.imapuids[i],
                                   client->watch.journal.flags[i]);
            } else if (STORE_WATCH_EVENT_COLL_REMOVED == event) {
                ccode = ConnWriteF(client->conn, 
                                   "6000 REMOVED " GUID_FMT "\r\n",
                                   client->watch.collection);
                if (StoreWatcherRemove(client, client->watchLock)) {
                    ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
                }
            } else if (STORE_WATCH_EVENT_COLL_RENAMED == event) {
                ccode = ConnWriteF(client->conn, 
                                   "6000 RENAMED " GUID_FMT "\r\n",
                                   client->watch.collection);
            } else {                                  
                ccode = ConnWriteF(client->conn, 
                                   "6000 %s " GUID_FMT " %08x\r\n", 
                                   STORE_WATCH_EVENT_DELETED == event ? "DELETED" :
                                   STORE_WATCH_EVENT_MODIFIED == event ? "MODIFIED" :
                                   STORE_WATCH_EVENT_NEW == event ? "NEW" : "ERROR",
                                   client->watch.journal.guids[i],
                                   client->watch.journal.imapuids[i]);
            }
        }
    }

    client->watch.journal.count = 0;

    StoreReleaseExclusiveLock(client, client->watchLock);
    /* we maintain the read lock until we are done processing the current command */
    return ccode;
}


/* watch event lists */

typedef struct {
    NLockStruct *lock;
    StoreWatchEvents event;
    uint64_t guid;
    uint64_t imapuid;
    uint32_t flags;
} eventdatum;


int 
WatchEventListInit(WatchEventList *events)
{
    return BongoArrayInit(events, sizeof(eventdatum), 32);
}


void
WatchEventListDestroy(WatchEventList *events)
{
    BongoArrayDestroy(events, TRUE);
}


int
WatchEventListAdd(WatchEventList *events, 
                  NLockStruct *lock, StoreWatchEvents event,
                  uint64_t guid, uint32_t imapuid, uint32_t flags)
{
    eventdatum evt;

    evt.lock = lock;
    evt.event = event;
    evt.guid = guid;
    evt.imapuid = imapuid;
    evt.flags = flags;

    BongoArrayAppendValue(events, evt);
    
    return 0;
}


void
WatchEventListFire(WatchEventList *events, StoreClient *client)
{
    unsigned int i;

    for (i = 0; i < events->len; i++) {
        eventdatum evt = BongoArrayIndex(events, eventdatum, i);
        
        StoreWatcherEvent(client, evt.lock, 
                          evt.event, 
                          evt.guid, evt.imapuid, evt.flags);
    }
}
