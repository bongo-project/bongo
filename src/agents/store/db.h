
#ifndef BONGODB_H
#define BONGODB_H

#include <sqlite3.h>

XPL_BEGIN_C_LINKAGE

#define STORE_GUID_LIST_MAX 500

/* these defs should be treated as opaque ptrs */
typedef struct _DStoreHandle DStoreHandle;
typedef struct _DStoreStmt DStoreStmt;
#define DSTORE_INVALID_STMT NULL

/*  1 row returned
    0 no row found
   -1 db error 
   -2 db locked
*/
typedef int DCode;

typedef int InfoStmtFilter(DStoreDocInfo *info, void *userdata);


DStoreHandle *DStoreOpen(char *basepath, BongoMemStack *memstack, int locktimeoutms);
void DStoreClose(DStoreHandle *handle);

BongoMemStack *DStoreGetMemStack(DStoreHandle *handle);
void DStoreSetMemStack(DStoreHandle *handle, BongoMemStack *memstack);


void DStoreSetLockTimeout(DStoreHandle *handle, int timeoutms);

void DStoreInfoStmtAddFilter(DStoreStmt *stmt, 
                             InfoStmtFilter *filter, void *userdata);

/* returns: 1 on success
            0 if no row available,
            -1 on error
*/

int DStoreStmtStep(DStoreHandle *handle, 
                   DStoreStmt *stmt);

int DStoreStmtExecute(DStoreHandle *handle, DStoreStmt *stmt);

void DStoreStmtEnd(DStoreHandle *handle, DStoreStmt *stmt);

void DStoreStmtError(DStoreHandle *handle, DStoreStmt *stmt);


/* prints db error to console */
int DStoreStmtPrintError(DStoreHandle *handle);



int DStoreBeginTransaction(DStoreHandle *handle);

int DStoreBeginSharedTransaction(DStoreHandle *handle);

int DStoreCommitTransaction(DStoreHandle *handle);

int DStoreAbortTransaction(DStoreHandle *handle);

int DStoreCancelTransactions(DStoreHandle *handle);

/* info table: */

/* guid and/or resource can be null */
DStoreStmt * DStoreFindDocInfo(DStoreHandle *handle,
                               uint64_t guid, 
                               char *coll,
                               char *filename);

int DStoreInfoStmtStep(DStoreHandle *handle, 
                       DStoreStmt *stmt,
                       DStoreDocInfo *info);

int DStoreGetDocInfoGuid(DStoreHandle *handle,
                         uint64_t guid,
                         DStoreDocInfo *info);

int DStoreGetDocInfoFilename(DStoreHandle *handle,
                             char *filename,
                             DStoreDocInfo *info);

int StoreFindCollectionGuid(DStoreHandle *handle, char *collection, 
                            StoreDocumentType type, uint64_t *outGuid);

int DStoreSetDocInfo(DStoreHandle *handle,
                     DStoreDocInfo *info);

int DStoreDeleteDocInfo(DStoreHandle *handle,
                        uint64_t guid);

int DStoreDeleteCollectionContents(DStoreHandle *handle, uint64_t guid);

DStoreStmt *DStoreListColl(DStoreHandle *handle, uint64_t collection, int start, int end);

DStoreStmt *DStoreListUnindexed(DStoreHandle *handle, int value, uint64_t collection);

DStoreStmt *DStoreGuidList(DStoreHandle *handle, uint64_t *guids, int numGuids);

int DStoreSetIndexed(DStoreHandle *handle, int value, uint64_t collection);


int DStoreFindCollectionGuid (DStoreHandle *handle, char *collection, 
                              uint64_t *outGuid);

int DStoreFindDocumentGuid (DStoreHandle *handle, char *path, 
                            uint64_t *outGuid);

int DStoreGenerateGUID(DStoreHandle *handle, uint64_t *guidOut);

int DStoreRenameDocuments(DStoreHandle *handle, 
                          const char *oldprefix, const char *newprefix);


DStoreStmt *DStoreFindMessages(DStoreHandle *handle,
                               uint32_t requiredSources, uint32_t disallowedSources, 
                               StoreHeaderInfo *headers, int headercount,
                               uint64_t *guidset,
                               int start, int end, uint64_t centerGuid, 
                               uint32_t mask, uint32_t flags);

int DStoreCountMessages(DStoreHandle *handle,
                        uint32_t requiredSources, uint32_t disallowedSources, 
                        StoreHeaderInfo *headers, int headercount,
                        uint32_t mask, uint32_t flags);



/* tempdocs table: */

int DStoreResetTempDocsTable(DStoreHandle *handle);

int DStoreAddTempDocsInfo(DStoreHandle *handle, DStoreDocInfo *info);

int DStoreMergeTempDocsTable(DStoreHandle *handle);


/* properties table: */

DStoreStmt *DStoreFindProperties(DStoreHandle *handle,
                                 uint64_t guid,
                                 char *propname);

int DStorePropStmtStep(DStoreHandle *handle, 
                       DStoreStmt *_stmt,
                       StorePropInfo *prop);

int DStoreGetProperty(DStoreHandle *handle,
                      uint64_t guid, char *propname, char *buffer, size_t size);

int DStoreGetPropertySBAppend(DStoreHandle *handle,
                              uint64_t guid, char *propname, BongoStringBuilder *sb);

int DStoreSetProperty(DStoreHandle *handle, 
                      uint64_t guid, StorePropInfo *prop);

int DStoreSetPropertySimple(DStoreHandle *handle,
                            uint64_t guid, 
                            const char *name,
                            const char *value);

/* Conversations table */
DStoreStmt *DStoreListConversations(DStoreHandle *handle, 
                                    uint32_t requiredSources, 
                                    uint32_t disallowedSources, 
                                    StoreHeaderInfo *headers, 
                                    int headercount,
                                    int start, 
                                    int end,
                                    uint64_t centerGuid,
                                    uint32_t mask,
                                    uint32_t flags);

int DStoreCountConversations(DStoreHandle *handle, 
                             uint32_t requiredSources,
                             uint32_t disallowedSources,
                             StoreHeaderInfo *headers, 
                             int hdrscount,
                             uint32_t mask, 
                             uint32_t flags);

DStoreStmt *DStoreFindConversation(DStoreHandle *handle,
                                   const char *subject,
                                   int32_t modifiedAfter);
int DStoreGetConversation(DStoreHandle *handle,
                          const char *subject,
                          int32_t modifiedAfter,
                          DStoreDocInfo *result);

DStoreStmt *DStoreFindConversationMembers(DStoreHandle *handle,
                                          uint64_t conversationId);

DStoreStmt *DStoreFindConversationSenders(DStoreHandle *handle, 
                                          uint64_t conversation);

DStoreStmt *DStoreFindMailingLists(DStoreHandle *handle, uint32_t requiredSources);

int DStoreStrStmtStep(DStoreHandle *handle, DStoreStmt *stmt, const char **strOut);

int DStoreGuidStmtStep(DStoreHandle *handle, DStoreStmt *stmt, uint64_t *guidOut);


/* events table */

int DStoreSetEvent(DStoreHandle *handle, DStoreDocInfo *info);

int DStoreDelEvent(DStoreHandle *handle, DStoreDocInfo *info);

/* links table */

DStoreStmt *DStoreFindEvents(DStoreHandle *handle, 
                             char *start, char *end,
                             uint64_t calendar, uint32_t mask,
                             char *uid);

int DStoreEventStmtStep(DStoreHandle *handle, DStoreStmt *stmt, DStoreDocInfo *info);

int DStoreLinkEvent(DStoreHandle *handle, uint64_t calendar, uint64_t event);

int DStoreUnlinkEvent(DStoreHandle *handle, uint64_t calendar, uint64_t event);
int DStoreUnlinkCalendar(DStoreHandle *handle, uint64_t calendar);

int DStoreFindCalendars(DStoreHandle *handle, uint64_t event, BongoArray *calsOut);

/* journal table */

int DStoreAddJournalEntry(DStoreHandle *handle, uint64_t collection, char *filename);

int DStoreRemoveJournalEntry(DStoreHandle *handle, uint64_t collection);

DStoreStmt *DStoreListJournal(DStoreHandle *handle);

int DStoreJournalStmtStep(DStoreHandle *handle, DStoreStmt *stmt, 
                          uint64_t *outGuid, const char **outFilename);


/* mime_cache table */

#include "mime.h"

DCode DStoreGetMimeReport(DStoreHandle *handle, 
                          uint64_t guid, MimeReport **outReport);

DCode DStoreSetMimeReport(DStoreHandle *handle, 
                          uint64_t guid, MimeReport *report);

int DStoreClearMimeReport(DStoreHandle *handle, uint64_t guid);

/* dbpool.c */

int DBPoolInit(void);
DStoreHandle *DBPoolGet(const char *user);
int DBPoolPut(const char *user, DStoreHandle *handle);

XPL_END_C_LINKAGE
#endif
