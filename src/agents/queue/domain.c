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
#include <logger.h>

#include "domain.h"

#if 0

typedef enum {
    QDBHANDLE_FLAG_INUSE = (1 << 0), 
    QDBHANDLE_FLAG_NON_POOLABLE = (1 << 1)
} QDBHandleFlags;

typedef struct _QDBHandle {
    struct _QDBHandle *next;
    struct _QDBHandle *previous;

    QDBHandleFlags flags;

    sqlite3 *handle;
} QDBHandle;

struct {
    QDBHandle *available;
    QDBHandle *inUse;

    XplSemaphore sem;

    struct {
        unsigned int minimum;
        unsigned int maximum;

        unsigned int allocated;
    } eCount;

    struct {
        unsigned long pitches;
        unsigned long strikes;
    } stats;
} QDBHandles;

static int 
QDBSearchCB(void *param, int cCount, char **cValues, char **cNames)
{
    int i;
    MDBValueStruct *vs = (MDBValueStruct *)param;

    for (i = 0; i < cCount; i++) {
        MDBAddValue(cValues[i], vs);
    }

    return(0);
}

static int 
QueryResultsCB(void *param, int cCount, char **cValues, char **cNames)
{
    int i;
    QueryResults *qData = (QueryResults *)param;

    if (qData->column.count == 0) {
        qData->column.count = cCount;

        for (i = 0; i < cCount; i++) {
            MDBAddValue(cNames[i], qData->column.names);
        }
    }

    qData->row.count++;
    for (i = 0; i < cCount; i++) {
        if (cValues[1]) {
            MDBAddValue(cValues[i], qData->row.values);
        } else {
            MDBAddValue("NULL", qData->row.values);
        }
    }

    return(0);
}

#endif

int 
QDBStartup(int minimum, int maximum)
{
    return 0;
#if 0
    int i;
    int ccode = SQLITE_ERROR;
    unsigned char *ptr;
    char *errmsg = NULL;
    sqlite3 *handle = NULL;
    QDBHandle *qdb;

    memset(&QDBHandles, 0, sizeof(QDBHandles));

    XplOpenLocalSemaphore(QDBHandles.sem, 0);

    QDBHandles.eCount.minimum = minimum;
    QDBHandles.eCount.maximum = maximum;

    if (access(NMAP.path.qDomains, 0) != 0) {
        ccode = sqlite3_open(NMAP.path.qDomains, &handle);
        if (ccode == SQLITE_OK) {
            ccode = sqlite3_exec(handle, NMAP_QDB_SQLITE_CREATE, NULL, NULL, &errmsg);
            if (ccode == SQLITE_OK) {
                ccode = sqlite3_exec(handle, NMAP_QDB_SQLITE_INDEX, NULL, NULL, &errmsg);
                if (ccode != SQLITE_OK) {
                    ptr = "index";
                }
            } else {
                ptr = "table";
            }
        } else {
            ptr = "database";
        }

        if (ccode != SQLITE_OK) {
            if (errmsg) {
                XplConsolePrintf("bongonmap: Failed to create NMAP QDB %s; error: \"%s\".\r\n", ptr, errmsg);
            } else {
                XplConsolePrintf("bongonmap: Failed to create NMAP QDB %s; error %d.\r\n", ptr, ccode);
            }
        }

        if (errmsg) {
            sqlite3_free(errmsg);
        }

        if (handle) {
            sqlite3_close(handle);
            handle = NULL;
        }

        if (ccode != SQLITE_OK) {
            return(-1);
        }
    }

    for (i = 0; i < maximum; i++) {
        qdb = (QDBHandle *)MemMalloc(sizeof(QDBHandle));
        if (qdb) {
            memset(qdb, 0, sizeof(QDBHandle));

            if (sqlite3_open(NMAP.path.qDomains, &(qdb->handle)) == SQLITE_OK) {
                if ((qdb->next = QDBHandles.available) != NULL) {
                    QDBHandles.available->previous = qdb;
                }
                QDBHandles.available = qdb;

                continue;
            }
            
            if (qdb->handle) {
                sqlite3_close(qdb->handle);
            }

            MemFree(qdb);
        }

        break;
    }

    if (i) {
        XplSignalLocalSemaphore(QDBHandles.sem);

        return(0);
    }

    return(-1);
#endif
}

void 
QDBShutdown(void)
{
#if 0
    QDBHandle *qdb;
    QDBHandle *next;

    XplWaitOnLocalSemaphore(QDBHandles.sem);

    qdb = QDBHandles.available;
    QDBHandles.available = NULL;

    while (qdb) {
        next = qdb->next;

        sqlite3_close(qdb->handle);

        qdb = next;
    }

    qdb = QDBHandles.inUse;
    QDBHandles.inUse = NULL;

    while (qdb) {
        next = qdb->next;

        sqlite3_close(qdb->handle);

        qdb = next;
    }

    XplCloseLocalSemaphore(QDBHandles.sem);
#endif
}

void *
QDBHandleAlloc(void)
{
    return NULL;
#if 0
    QDBHandle *qdb;

    XplWaitOnLocalSemaphore(QDBHandles.sem);

    QDBHandles.stats.pitches++;

    if (QDBHandles.available != NULL) {
        qdb = QDBHandles.available;
        if ((QDBHandles.available = qdb->next) != NULL) {
            QDBHandles.available->previous = NULL;
        }

        qdb->previous = NULL;
        if ((qdb->next = QDBHandles.inUse) != NULL) {
            QDBHandles.inUse->previous = qdb;
        }
        QDBHandles.inUse = qdb;

        XplSignalLocalSemaphore(QDBHandles.sem);

        qdb->flags |= QDBHANDLE_FLAG_INUSE;

        return(qdb);
    }

    QDBHandles.stats.strikes++;

    XplSignalLocalSemaphore(QDBHandles.sem);

    qdb = (QDBHandle *)MemMalloc(sizeof(QDBHandle));
    if (qdb) {
        memset(qdb, 0, sizeof(QDBHandle));

        if (sqlite3_open(NMAP.path.qDomains, &(qdb->handle)) == SQLITE_OK) {
            XplWaitOnLocalSemaphore(QDBHandles.sem);

            QDBHandles.eCount.allocated++;
            if (QDBHandles.eCount.allocated < QDBHandles.eCount.maximum) {
                qdb->flags = QDBHANDLE_FLAG_INUSE;
            } else {
                qdb->flags = QDBHANDLE_FLAG_INUSE | QDBHANDLE_FLAG_NON_POOLABLE;
            }

            if ((qdb->next = QDBHandles.inUse) != NULL) {
                QDBHandles.inUse->previous = qdb;
            }
            QDBHandles.inUse = qdb;

            XplSignalLocalSemaphore(QDBHandles.sem);

            return(qdb);
        }

        if (qdb->handle) {
            sqlite3_close(qdb->handle);
        }

        MemFree(qdb);
    }

    return(NULL);
#endif
}

void 
QDBHandleRelease(void *handle)
{
#if 0
    QDBHandle *qdb = handle;

    qdb->flags &= ~QDBHANDLE_FLAG_INUSE;

    XplWaitOnLocalSemaphore(QDBHandles.sem);
    if (qdb->next != NULL) {
        qdb->next->previous = qdb->previous;
    }

    if (qdb->previous != NULL) {
        qdb->previous->next = qdb->next;
    } else {
        QDBHandles.inUse = qdb->next;
    }

    if (!(qdb->flags & QDBHANDLE_FLAG_NON_POOLABLE)) {
        qdb->previous = NULL;
        if ((qdb->next = QDBHandles.available) != NULL) {
            QDBHandles.available->previous = qdb;
        }

        QDBHandles.available = qdb;
    } else {
        sqlite3_close(qdb->handle);

        MemFree(qdb);
    }

    XplSignalLocalSemaphore(QDBHandles.sem);
#endif
}

int 
QDBAdd(void *handle, unsigned char *domain, unsigned long queueID, int queue)
{
    return -1;

#if 0
    int ccode;
    char *errmsg = NULL;
    char buffer[MAXEMAILNAMESIZE + 96];
    QDBHandle *qdb = (QDBHandle *)handle;

    sprintf(buffer, "INSERT INTO DOMAINS (DOMAIN, ENTRYID, ENTRYQ) VALUES ('%s', %lu, %d);", domain, queueID, queue);
    ccode = sqlite3_exec(qdb->handle, buffer, NULL, NULL, &errmsg);
    if (ccode != SQLITE_OK) {
        if (errmsg) {
            XplConsolePrintf("bongonmap: Failed to insert NMAP QDB '%s' %08x.%03d; error: \"%s\".\r\n", domain, (unsigned int)queueID, queue, errmsg);
        } else {
            XplConsolePrintf("bongonmap: Failed to insert NMAP QDB '%s' %08x.%03d; error %d.\r\n", domain, (unsigned int)queueID, queue, ccode);
        }
    }

    if (errmsg) {
        sqlite3_free(errmsg);
    }

    if (ccode == SQLITE_OK) {
        return(0);
    }

    return(-1);
#endif
}

int 
QDBRemoveID(void *handle, unsigned long queueID)
{
    return -1;
    
#if 0
    int ccode;
    char *errmsg = NULL;
    char buffer[MAXEMAILNAMESIZE + 48];
    QDBHandle *qdb = (QDBHandle *)handle;

    sprintf(buffer, "DELETE FROM DOMAINS WHERE ENTRYID = %lu;", queueID);
    ccode = sqlite3_exec(qdb->handle, buffer, NULL, NULL, &errmsg);
    if (ccode != SQLITE_OK) {
        if (errmsg) {
            XplConsolePrintf("bongonmap: Failed to delete NMAP QDB entry %08x; error: \"%s\".\r\n", (unsigned int)queueID, errmsg);
        } else {
            XplConsolePrintf("bongonmap: Failed to delete NMAP QDB entry %08x; error %d.\r\n", (unsigned int)queueID, ccode);
        }
    }

    if (errmsg) {
        sqlite3_free(errmsg);
    }

    if (ccode == SQLITE_OK) {
        return(0);
    }

    return(-1);
#endif
}

int 
QDBRemoveDomain(void *handle, unsigned char *domain)
{
    return -1;

#if 0
    int ccode;
    char *errmsg = NULL;
    char buffer[MAXEMAILNAMESIZE + 48];
    QDBHandle *qdb = (QDBHandle *)handle;

    sprintf(buffer, "DELETE FROM DOMAINS WHERE ENTRY = '%s';", domain);
    ccode = sqlite3_exec(qdb->handle, buffer, NULL, NULL, &errmsg);
    if (ccode != SQLITE_OK) {
        if (errmsg) {
            XplConsolePrintf("bongonmap: Failed to delete NMAP QDB entry \"%s\"; error: \"%s\".\r\n", domain, errmsg);
        } else {
            XplConsolePrintf("bongonmap: Failed to delete NMAP QDB entry \"%s\"; error %d.\r\n", domain, ccode);
        }
    }

    if (errmsg) {
        sqlite3_free(errmsg);
    }

    if (ccode == SQLITE_OK) {
        return(0);
    }

    return(-1);
#endif
}

int 
QDBSearchID(void *handle, unsigned long queueID, MDBValueStruct *vs)
{
    return -1;
    
#if 0
    int ccode;
    char *errmsg = NULL;
    char buffer[MAXEMAILNAMESIZE + 48];
    QDBHandle *qdb = (QDBHandle *)handle;

    sprintf(buffer, "SELECT DOMAIN FROM DOMAINS WHERE ENTRYID = %lu;", queueID);
    ccode = sqlite3_exec(qdb->handle, buffer, QDBSearchCB, (void *)vs, &errmsg);
    if (ccode != SQLITE_OK) {
        if (errmsg) {
            XplConsolePrintf("bongonmap: Failed search for NMAP QDB entry %08x; error: \"%s\".\r\n", (unsigned int)queueID, errmsg);
        } else {
            XplConsolePrintf("bongonmap: Failed search for NMAP QDB entry %08x; error %d.\r\n", (unsigned int)queueID, ccode);
        }
    }

    if (errmsg) {
        sqlite3_free(errmsg);
    }

    if (ccode == SQLITE_OK) {
        return(0);
    }

    return(-1);
#endif
}

int 
QDBSearchDomain(void *handle, unsigned char *domain, MDBValueStruct *vs)
{
    return -1;
#if 0
    int ccode;
    char *errmsg = NULL;
    char buffer[MAXEMAILNAMESIZE + 48];
    QDBHandle *qdb = (QDBHandle *)handle;

    sprintf(buffer, "SELECT ENTRYID FROM DOMAINS WHERE DOMAIN = '%s';", domain);
    ccode = sqlite3_exec(qdb->handle, buffer, QDBSearchCB, (void *)vs, &errmsg);
    if (ccode != SQLITE_OK) {
        if (errmsg) {
            XplConsolePrintf("bongonmap: Failed search for NMAP QDB entry \"%s\"; error: \"%s\".\r\n", domain, errmsg);
        } else {
            XplConsolePrintf("bongonmap: Failed search for NMAP QDB entry \"%s\"; error %d.\r\n", domain, ccode);
        }
    }

    if (errmsg) {
        sqlite3_free(errmsg);
    }

    if (ccode == SQLITE_OK) {
        return(0);
    }

    return(-1);
#endif
}

int 
QDBQuery(void *handle, unsigned char *query, QDBQueryResults *results)
{
    return -1;
#if 0
    int ccode;
    char *errmsg = NULL;
    QDBHandle *qdb = (QDBHandle *)handle;

    ccode = sqlite3_exec(qdb->handle, query, QDBQueryResultsCB, (void *)results, &errmsg);
    if (ccode != SQLITE_OK) {
        if (errmsg) {
            XplConsolePrintf("bongonmap: Failed query on NMAP QDB for \"%s\"; error: \"%s\".\r\n", query, errmsg);
        } else {
            XplConsolePrintf("bongonmap: Failed query on NMAP QDB for \"%s\"; error %d.\r\n", query, ccode);
        }
    }

    if (errmsg) {
        sqlite3_free(errmsg);
    }

    if (ccode == SQLITE_OK) {
        return(0);
    }

    return(-1);
#endif
}

int 
QDBDump(unsigned char *domain)
{
    return -1;
    
#if 0
    int ccode;
    unsigned long used;
    unsigned long queueID;
    unsigned long *idLock;
    char *errmsg = NULL;
    unsigned char path[XPL_MAX_PATH + 1];
    char buffer[MAXEMAILNAMESIZE + 48];
    MDBValueStruct *vs;
    QDBHandle *qdb;

    if (domain) {
        XplConsolePrintf("bongonmap: Removing queued entries for domain: %s\r\n", domain);
    } else {
        XplConsolePrintf("bongonmap: Removing all queued entries\r\n");
    }

    vs = MDBCreateValueStruct(NMAP.handle.directory, NULL);
    if (vs) {
        qdb = QDBHandleAlloc();
    } else {
        LoggerEvent(NMAP.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_OUT_OF_MEMORY, LOG_CRITICAL, 0, __FILE__, NULL, sizeof(MDBValueStruct), 0, NULL, 0);
        return(-1);
    }

    if (qdb) {
        if (domain) {
            sprintf(buffer, "SELECT ENTRYID FROM DOMAINS WHERE DOMAIN = '%s';", domain);
        } else {
            strcpy(buffer, "SELECT ENTRYID FROM DOMAINS;");
        }

        ccode = sqlite3_exec(qdb->handle, buffer, QDBSearchCB, (void *)vs, &errmsg);
        if (ccode != SQLITE_OK) {
            if (errmsg) {
                XplConsolePrintf("bongonmap: Failed search for NMAP QDB entry \"%s\"; error: \"%s\".\r\n", domain, errmsg);
            } else {
                XplConsolePrintf("bongonmap: Failed search for NMAP QDB entry \"%s\"; error %d.\r\n", domain, ccode);
            }
        }

        if (errmsg) {
            sqlite3_free(errmsg);
        }
    } else {
        LoggerEvent(NMAP.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_OUT_OF_MEMORY, LOG_CRITICAL, 0, __FILE__, NULL, sizeof(QDBHandle), 0, NULL, 0);
        return(-1);
    }

    ccode = 0;
    for (used = 0; (ccode != -1) && (used < vs->Used); used++) {
        queueID = atol(vs->Value[used]);

        idLock = SpoolEntryIDLock(queueID);
        if (idLock) {
            sprintf(path, "%s/c%07x.007", NMAP.path.spool, queueID);
            unlink(path);

            sprintf(path, "%s/d%s.msg", NMAP.path.spool, vs->Value[used]);
            unlink(path);

            QDBRemoveID(qdb, queueID);

            XplSafeDecrement(NMAP.stats.queuedLocal.messages);

            SpoolEntryIDUnlock(idLock);
        }
    }

    QDBHandleRelease(qdb);

    MDBDestroyValueStruct(vs);

    return(used);
#endif
}

void
QDBSummarizeQueue(void)
{
#if 0
    int ccode;
    unsigned long used;
    char *errmsg;
    unsigned char path[XPL_MAX_PATH + 1];
    FILE *fh;
    MDBValueStruct *vs;
    QDBHandle *qdb;

    vs = MDBCreateValueStruct(NMAP.handle.directory, NULL);
    if (vs) {
        qdb = QDBHandleAlloc();
    } else {
        LoggerEvent(NMAP.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_OUT_OF_MEMORY, LOG_CRITICAL, 0, __FILE__, NULL, sizeof(MDBValueStruct), 0, NULL, 0);
        return;
    }

    if (qdb) {
        ccode = sqlite3_exec(qdb->handle, "SELECT DOMAIN, COUNT(DOMAIN) FROM DOMAINS GROUP BY DOMAIN;", QDBSearchCB, (void *)vs, &errmsg);
        if (ccode != SQLITE_OK) {
            if (errmsg) {
                XplConsolePrintf("bongonmap: Failed count for NMAP QDB domains; error: \"%s\".\r\n", errmsg);
            } else {
                XplConsolePrintf("bongonmap: Failed count for NMAP QDB domains; error %d.\r\n", ccode);
            }
        }

        if (errmsg) {
            sqlite3_free(errmsg);
        }

        QDBHandleRelease(qdb);
    } else {
        LoggerEvent(NMAP.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_NMAP_OUT_OF_MEMORY, LOG_CRITICAL, 0, __FILE__, NULL, sizeof(QDBHandle), 0, NULL, 0);
        return;
    }

    if (vs->Used) {
        sprintf(path, "%s/queue.ims", NMAP.path.dbf);
        fh = fopen(path, "wb");
        if (fh) {
            fprintf(fh, "Domains:\r\n");

            ccode = 0;
            for (used = 0; (ccode != -1) && (used < vs->Used); used += 2) {
                fprintf(fh, "[%6lu] %s\r\n", atol(vs->Value[used + 1]), vs->Value[used]);
            }

            fclose(fh);
        }
    }

    MDBDestroyValueStruct(vs);
#endif
}
