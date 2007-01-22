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
#include <mdb.h>

#include "stored.h"

#define PRODUCT_NAME "Bongo NMAP Store Agent"
#define PRODUCT_DESCRIPTION "Provides mail-store access via the NMAP protocol."
#define PRODUCT_VERSION "$Revision: 2.0 $"

static BOOL ReadStoreVariable(unsigned int Variable, unsigned char *Data, size_t *DataLength);
static BOOL WriteStoreVariable(unsigned int Variable, unsigned char *Data, size_t DataLength);

static BOOL StoreShutdown(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL StoreDMCCommandHelp(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL SendStoreStatistics(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL DumpConnectionList(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);

ManagementVariables StoreManagementVariables[] = {
    { DMCMV_MESSAGING_SERVER_DN, DMCMV_MESSAGING_SERVER_DN_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_HOSTNAME, DMCMV_HOSTNAME_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_POSTMASTER, DMCMV_POSTMASTER_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_OFFICIAL_NAME, DMCMV_OFFICIAL_NAME_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_QUEUE_THREAD_COUNT, DMCMV_QUEUE_THREAD_COUNT_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_QUEUE_SLEEP_INTERVAL, NMAPMV_QUEUE_SLEEP_INTERVAL_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { DMCMV_MAIL_DIRECTORY, DMCMV_MAIL_DIRECTORY_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_SCMS_DIRECTORY, DMCMV_SCMS_DIRECTORY_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_SPOOL_DIRECTORY, DMCMV_SPOOL_DIRECTORY_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_DBF_DIRECTORY, DMCMV_DBF_DIRECTORY_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_BYTES_PER_BLOCK, NMAPMV_BYTES_PER_BLOCK_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_TRUSTED_HOSTS, NMAPMV_TRUSTED_HOSTS_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_TRUSTED_HOST_COUNT, NMAPMV_TRUSTED_HOST_COUNT_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_SCMS_USER_THRESHOLD, NMAPMV_SCMS_USER_THRESHOLD_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_SCMS_SIZE_THRESHOLD, NMAPMV_SCMS_SIZE_THRESHOLD_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_USE_SYSTEM_QUOTA, NMAPMV_USE_SYSTEM_QUOTA_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_USE_USER_QUOTA, NMAPMV_USE_USER_QUOTA_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_QUOTA_MESSAGE, NMAPMV_QUOTA_MESSAGE_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_QUOTA_WARNING, NMAPMV_QUOTA_WARNING_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_MINIMUM_FREE_SPACE, NMAPMV_MINIMUM_FREE_SPACE_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_DISK_SPACE_LOW, NMAPMV_DISK_SPACE_LOW_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_DO_DEFERRED_DELIVERY, NMAPMV_DO_DEFERRED_DELIVERY_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_LOAD_MONITOR_DISABLED, NMAPMV_LOAD_MONITOR_DISABLED_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_LOAD_MONITOR_INTERVAL, NMAPMV_LOAD_MONITOR_INTERVAL_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_LOW_UTILIZATION_THRESHOLD, NMAPMV_LOW_UTILIZATION_THRESHOLD_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_HIGH_UTILIZATION_THRESHOLD, NMAPMV_HIGH_UTILIZATION_THRESHOLD_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_QUEUE_LOAD_THRESHOLD, NMAPMV_QUEUE_LOAD_THRESHOLD_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_NEW_SHARE_MESSAGE, NMAPMV_NEW_SHARE_MESSAGE_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_USER_NAMESPACE_PREFIX, NMAPMV_USER_NAMESPACE_PREFIX_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_QUEUE_LIMIT_CONCURRENT, NMAPMV_QUEUE_LIMIT_CONCURRENT_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_QUEUE_LIMIT_SEQUENTIAL, NMAPMV_QUEUE_LIMIT_SEQUENTIAL_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_RTS_HANDLING, NMAPMV_RTS_HANDLING_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_RTS_MAX_BODY_SIZE, NMAPMV_RTS_MAX_BODY_SIZE_HELP, ReadStoreVariable, NULL }, 
    { NMAPMV_FORWARD_UNDELIVERABLE_ADDR, NMAPMV_FORWARD_UNDELIVERABLE_ADDR_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_FORWARD_UNDELIVERABLE, NMAPMV_FORWARD_UNDELIVERABLE_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_BLOCK_RTS_SPAM, NMAPMV_BLOCK_RTS_SPAM_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_BOUNCE_INTERVAL, NMAPMV_BOUNCE_INTERVAL_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_MAX_BOUNCE_COUNT, NMAPMV_MAX_BOUNCE_COUNT_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_NMAP_TO_NMAP_CONNECTIONS, NMAPMV_NMAP_TO_NMAP_CONNECTIONS_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_BYTES_STORED_LOCALLY, NMAPMV_BYTES_STORED_LOCALLY_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_MESSAGES_STORED_LOCALLY, NMAPMV_MESSAGES_STORED_LOCALLY_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_RECIPIENTS_STORED_LOCALLY, NMAPMV_RECIPIENTS_STORED_LOCALLY_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_MESSAGES_QUEUED_LOCALLY, NMAPMV_MESSAGES_QUEUED_LOCALLY_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_SPAM_MESSAGES_DISCARDED, NMAPMV_SPAM_MESSAGES_DISCARDED_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_LOCAL_DELIVERY_FAILURES, NMAPMV_LOCAL_DELIVERY_FAILURES_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_REMOTE_DELIVERY_FAILURES, NMAPMV_REMOTE_DELIVERY_FAILURES_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_REGISTERED_AGENTS_ARRAY, NMAPMV_REGISTERED_AGENTS_ARRAY_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_VERSION, DMCMV_VERSION_HELP, ReadStoreVariable, NULL }, 
    { DMCMV_TOTAL_CONNECTIONS, DMCMV_TOTAL_CONNECTIONS_HELP, ReadStoreVariable, WriteStoreVariable }, 
    { NMAPMV_AGENTS_SERVICED, NMAPMV_AGENTS_SERVICED_HELP, ReadStoreVariable, WriteStoreVariable }, 
};

ManagementCommands StoreManagementCommands[] = {
    { DMCMC_HELP, StoreDMCCommandHelp }, 
    { DMCMC_SHUTDOWN, StoreShutdown },
    { DMCMC_STATS, SendStoreStatistics }, 
    { DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats }, 
    { "ConnDump", DumpConnectionList }, 
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};

ManagementVariables *
GetStoreManagementVariables(void)
{
    return(StoreManagementVariables);
}

int 
GetStoreManagementVariablesCount(void)
{
    return(sizeof(StoreManagementVariables) / sizeof(ManagementVariables));
}

ManagementCommands *
GetStoreManagementCommands(void)
{
    return(StoreManagementCommands);
}

int 
GetStoreManagementCommandsCount(void)
{
    return(sizeof(StoreManagementCommands) / sizeof(ManagementCommands));
}

static BOOL 
StoreShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
//    XplThreadID id;

    if (response) {
        if (!arguments) {
            if (StoreAgent.server.conn) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
//                    id = XplSetThreadGroupID(StoreAgent.id.group);

                    StoreAgent.agent.state = BONGO_AGENT_STATE_STOPPING;

                    ConnClose(StoreAgent.server.conn, 1);

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }

//                    XplSetThreadGroupID(id);
                }
            } else if (StoreAgent.agent.state == BONGO_AGENT_STATE_STOPPING) {
                *response = MemStrdup("Shutdown in progress.\r\n");
            } else {
                *response = MemStrdup("Unknown shutdown state.\r\n");
            }

            if (*response) {
                return(TRUE);
            }

            return(FALSE);
        }

        *response = MemStrdup("arguments not allowed.\r\n");
        return(TRUE);
    }

    return(FALSE);
}

static BOOL 
StoreDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    BOOL responded = FALSE;

    if (response) {
        if (arguments) {
            switch(toupper(arguments[0])) {
                case 'Q': {
                    if (XplStrCaseCmp(arguments, "QDump") == 0) {
                        if ((*response = MemStrdup("Store an NMAP queued messages summary in the file queue.ims; in the configured DBF directory.\r\n")) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    } else if (XplStrCaseCmp(arguments, "QRemove") == 0) {
                        if ((*response = MemStrdup("Remove all NMAP queued messages for the specified domain.  The QRemove command and the domain to be removed should be seperated with a single space.\r\n")) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }

                    break;
                }

                case 'C': {
                    if (XplStrNCaseCmp(arguments, DMCMC_CONN_TRACE_USAGE, sizeof(DMCMC_CONN_TRACE_USAGE) - 1) == 0) {
                        if ((*response = MemStrdup(DMCMC_CONN_TRACE_USAGE_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                }

                case 'S': {
                    if (XplStrCaseCmp(arguments, DMCMC_SHUTDOWN) == 0) {
                        if ((*response = MemStrdup(DMCMC_SHUTDOWN_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    } else if (XplStrCaseCmp(arguments, DMCMC_STATS) == 0) {
                        if ((*response = MemStrdup(DMCMC_STATS_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                }

                default: {
                    break;
                }
            }
        } else if ((*response = MemStrdup(DMCMC_HELP_HELP)) != NULL) {
            responded = TRUE;
        }

        if (responded || ((*response = MemStrdup(DMCMC_UNKOWN_COMMAND)) != NULL)) {
            return(TRUE);
        }
    }

    return(FALSE);
}

static BOOL 
SendStoreStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    MemStatistics poolStats;
    BongoThreadPoolStatistics threadStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        BongoThreadPoolGetStatistics(StoreAgent.threadPool, &threadStats);
        MemPrivatePoolStatistics(StoreAgent.memPool, &poolStats);

        *response = MemMalloc(1024);

        if (*response) {

            snprintf (*response, 1024,
                      "%s (%s: v%d.%d.%d)\r\n"
                      "%lu:%lu:%lu:%lu"
                      ":%d:%d"
                      /* ":%d:%d" */
                      /* ":%d:%lu:%lu:%lu:%lu:%ld:%ld:%ld:%lu" */
                      /* ":%d:%d:%d:%d:%d:%d:%d:%d:%d:%d\r\n" */
                      ,

                      PRODUCT_NAME, 
                      PRODUCT_SHORT_NAME, 
                      PRODUCT_MAJOR_VERSION, 
                      PRODUCT_MINOR_VERSION, 
                      PRODUCT_LETTER_VERSION, 

                      poolStats.totalAlloc.count, 
                      poolStats.totalAlloc.size, 
                      poolStats.pitches, 
                      poolStats.strikes, 

                      threadStats.total,
                      threadStats.queueLength

/*
                      XplSafeRead(StoreAgent.server.active), 
                      XplSafeRead(StoreAgent.client.worker.active), 
                      XplSafeRead(StoreAgent.client.worker.idle), 
                      XplSafeRead(StoreAgent.queue.worker.active), 
                      
                      StoreAgent.loadMonitor.disabled, 
                      StoreAgent.loadMonitor.interval, 
                      StoreAgent.loadMonitor.low, 
                      StoreAgent.loadMonitor.high, 
                      StoreAgent.queue.limit.concurrent, 
                      StoreAgent.queue.limit.sequential, 
                      StoreAgent.queue.limit.trigger, 
                      StoreAgent.queue.bounce.interval, 
                      StoreAgent.queue.bounce.maxCount, 
                      
                      XplSafeRead(StoreAgent.stats.nmap.toNMAP), 
                      XplSafeRead(StoreAgent.stats.nmap.servicedNMAP), 
                      XplSafeRead(StoreAgent.stats.servicedAgents), 
                      XplSafeRead(StoreAgent.stats.queuedLocal.messages), 
                      XplSafeRead(StoreAgent.stats.spam), 
                      XplSafeRead(StoreAgent.stats.storedLocal.count), 
                      XplSafeRead(StoreAgent.stats.storedLocal.recipients), 
                      XplSafeRead(StoreAgent.stats.storedLocal.bytes), 
                      XplSafeRead(StoreAgent.stats.deliveryFailed.local), 
                      XplSafeRead(StoreAgent.stats.deliveryFailed.remote)
*/
                      );

            return(TRUE);
        }

        if ((*response = MemStrdup("Out of memory.\r\n")) != NULL) {
            return(TRUE);
        }
    } else if ((arguments) && ((*response = MemStrdup("arguments not allowed.\r\n")) != NULL)) {
        return(TRUE);
    }

    return(FALSE);
}


static BOOL 
DumpConnectionListCB(void *buffer, void *data)
{
    unsigned char *ptr;
    StoreClient *client = (StoreClient *)buffer;

    if (client) {
        ptr = (unsigned char *)data + strlen((unsigned char *)data);

        ptr += sprintf(ptr, "%d.%d.%d.%d:%d ", client->conn->socketAddress.sin_addr.s_net, client->conn->socketAddress.sin_addr.s_host, client->conn->socketAddress.sin_addr.s_lh, client->conn->socketAddress.sin_addr.s_impno, client->conn->socket);

        if (!client->flags) {
            ptr += sprintf(ptr, "[] ");
        } else if (IS_MANAGER(client)) {
            ptr += sprintf(ptr, "[S Manager] ");
//        } else if (client->flagss & (NMAP_CLIENT_USER | NMAP_CLIENT_MBOX)) {
//            ptr += sprintf(ptr, "[U %s] ", client->user);
        } else {
            ptr += sprintf(ptr, "[?] ");
        }

        ptr += sprintf(ptr, "\"%s\"\r\n", client->buffer);

        return(TRUE);
    }

    return(FALSE);
}

static BOOL 
DumpConnectionList(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
#if 0
    if (response) {
        if (!arguments) {
            *response = (unsigned char *)MemMalloc(XplSafeRead(StoreAgent.client.worker.active) * ((MAXEMAILNAMESIZE + CONN_BUFSIZE + 64) * sizeof(unsigned char)));

            if (*response) {
                **response = '\0';

                MemPrivatePoolEnumerate(StoreAgent.memPool, DumpConnectionListCB, (void *)*response);
            } else {
                *response = MemStrdup("Out of memory.\r\n");
            }
        } else {
            *response = MemStrdup("arguments not allowed.\r\n");
        }

        if (*response) {
            return(TRUE);
        }
    }
#endif
    return(FALSE);
}

static BOOL 
ReadStoreVariable(unsigned int variable, unsigned char *data, size_t *dataLength)
{
    size_t count;
    unsigned long index;
    unsigned char *ptr;
#if 0
    switch (variable) {
        case 0: {
            count = strlen(StoreAgent.server.dn) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.server.dn);
            }

            *dataLength = count;
            break;
        }

        case 1: {
            count = strlen(StoreAgent.server.host) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.server.host);
            }

            *dataLength = count;
            break;
        }

        case 2: {
            count = strlen(StoreAgent.postMaster) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.postMaster);
            }

            *dataLength = count;
            break;
        }

        case 3: {
            count = strlen(StoreAgent.officialName) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.officialName);
            }

            *dataLength = count;
            break;
        }

        case 4: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.server.active));
            }

            *dataLength = 12;
            break;
        }

        case 5: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.client.worker.active));
            }

            *dataLength = 12;
            break;
        }

        case 6: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.client.worker.idle));
            }

            *dataLength = 12;
            break;
        }

        case 7: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.queue.worker.active));
            }

            *dataLength = 12;
            break;
        }

        case 8: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.queue.sleep);
            }

            *dataLength = 12;
            break;
        }

        case 9: {
            count = strlen(StoreAgent.path.mail) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.path.mail);
            }

            *dataLength = count;
            break;
        }

        case 10: {
            count = strlen(StoreAgent.path.scms) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.path.scms);
            }

            *dataLength = count;
            break;
        }

        case 11: {
            count = strlen(StoreAgent.path.spool) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.path.spool);
            }

            *dataLength = count;
            break;
        }

        case 12: {
            count = strlen(StoreAgent.path.dbf) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.path.dbf);
            }

            *dataLength = count;
            break;
        }

        case 13: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.server.bytesPerBlock);
            }

            *dataLength = 12;
            break;
        }

        case 14: {
            XplRWReadLockAcquire(&StoreAgent.lock.config);

            if (StoreAgent.trusted.count) {
                if (data && (*dataLength > (size_t)(StoreAgent.trusted.count * 17))) {
                    ptr = data;
                    for (index = 0; index < StoreAgent.trusted.count; index++) {
                        ptr += sprintf(ptr, "%ld.%ld.%ld.%ld\r\n", StoreAgent.trusted.hosts[index] & 0xFF, (StoreAgent.trusted.hosts[index] & 0xFF00) >> 8, (StoreAgent.trusted.hosts[index] & 0xFF0000) >> 16, (StoreAgent.trusted.hosts[index] & 0xFF000000) >> 24);
                    }

                    *dataLength = ptr - data;
                } else {
                    *dataLength = StoreAgent.trusted.count * 17;
                }
            } else {
                if (data && (*dataLength > 40)) {
                    strcpy(data, "No trusted hosts have been configured.\r\n");
                }
                *dataLength = 40;
            }

            XplRWReadLockRelease(&StoreAgent.lock.config);

            break;
        }

        case 15: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.trusted.count);
            }

            *dataLength = 12;
            break;
        }

        case 16: {
            if (data && (*dataLength >= 12)) {
                sprintf(data, "%010d\r\n", StoreAgent.scms.userThreshold);
            }

            *dataLength = 12;
            break;
        }

        case 17: {
            if (data && (*dataLength >= 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.scms.sizeThreshold);
            }

            *dataLength = 12;
            break;
        }

        case 18: {
            if (StoreAgent.quota.useSystem == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 19: {
            if (StoreAgent.quota.useUser == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 20: {
            if (StoreAgent.quota.message && StoreAgent.quota.message[0]) {
                count = strlen(StoreAgent.quota.message) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", StoreAgent.quota.message);
                }
            } else {
                count = strlen(DEFAULT_QUOTA_MESSAGE) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", DEFAULT_QUOTA_MESSAGE);
                }
            }

            *dataLength = count;
            break;
        }

        case 21: {
            if (StoreAgent.quota.warning) {
                count = strlen(StoreAgent.quota.warning) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", StoreAgent.quota.warning);
                }
            } else {
                count = strlen(DEFAULT_QUOTA_WARNING) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", DEFAULT_QUOTA_WARNING);
                }
            }

            *dataLength = count;
            break;
        }

        case 22: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.queue.minimumFree);
            }

            *dataLength = 12;
            break;
        }

        case 23: {
            if (!(StoreAgent.flags & NMAP_FLAG_DISK_SPACE_LOW)) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 24: {
            if (!StoreAgent.defer.enabled) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 25: {
            if (StoreAgent.loadMonitor.disabled == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 26: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.loadMonitor.interval);
            }

            *dataLength = 12;
            break;
        }

        case 27: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.loadMonitor.low);
            }

            *dataLength = 12;
            break;
        }

        case 28: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.loadMonitor.high);
            }

            *dataLength = 12;
            break;
        }

        case 29: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010ld\r\n", StoreAgent.queue.limit.trigger);
            }

            *dataLength = 12;
            break;
        }

        case 30: {
            count = strlen(StoreAgent.newShareMessage) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.newShareMessage);
            }

            *dataLength = count;
            break;
        }

        case 31: {
            count = strlen(StoreAgent.nameSpace.userPrefix) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", StoreAgent.nameSpace.userPrefix);
            }

            *dataLength = count;
            break;
        }

        case 32: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010ld\r\n", StoreAgent.queue.limit.concurrent);
            }

            *dataLength = 12;
            break;
        }

        case 33: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010ld\r\n", StoreAgent.queue.limit.sequential);
            }

            *dataLength = 12;
            break;
        }

        case 34: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.rts.handling);
            }

            *dataLength = 12;
            break;
        }

        case 35: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.rts.maxBodySize);
            }

            *dataLength = 12;
            break;
        }

        case 36: {
            if (StoreAgent.queue.forwardUndeliverable.enabled) {
                count = strlen(StoreAgent.queue.forwardUndeliverable.address) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", StoreAgent.queue.forwardUndeliverable.address);
                }

                *dataLength = count;
                break;
            }

            if (data && (*dataLength > 2)) {
                strcpy(data, "\r\n");
            }

            *dataLength = 2;
            break;
        }

        case 37: {
            if (StoreAgent.queue.forwardUndeliverable.enabled == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 38: {
            if (StoreAgent.rts.blockSpam == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 39: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.queue.bounce.interval);
            }

            *dataLength = 12;
            break;
        }

        case 40: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", StoreAgent.queue.bounce.maxCount);
            }

            *dataLength = 12;
            break;
        }

        case 41: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.nmap.toNMAP));
            }

            *dataLength = 12;
            break;
        }

        case 42: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.storedLocal.bytes));
            }

            *dataLength = 12;
            break;
        }

        case 43: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.storedLocal.count));
            }

            *dataLength = 12;
            break;
        }

        case 44: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.storedLocal.recipients));
            }

            *dataLength = 12;
            break;
        }

        case 45: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.queuedLocal.messages));
            }

            *dataLength = 12;
            break;
        }

        case 46: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.spam));
            }

            *dataLength = 12;
            break;
        }

        case 47: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.deliveryFailed.local));
            }

            *dataLength = 12;
            break;
        }

        case 48: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.deliveryFailed.remote));
            }

            *dataLength = 12;
            break;
        }

        case 49: {
            if (StoreAgent.queue.clients.count) {
                unsigned long i;

                count = 0;
                for (i = 0; i < StoreAgent.queue.clients.count; i++) {
                    count += (15 + 3 + 3 + strlen(StoreAgent.queue.clients.array[i].identifier) + 28 + 1);
                }

                if (data && (*dataLength > count)) {
                    ptr = data;

                    for (i = 0; i < StoreAgent.queue.clients.count; i++) {
                        ptr += sprintf(ptr, "%ld.%ld.%ld.%ld Port %03d; Queue: %03d; Agent: \"%s\"\r\n", 
                            StoreAgent.queue.clients.array[i].address & 0xff, (StoreAgent.queue.clients.array[i].address & 0xff00) >> 8, (StoreAgent.queue.clients.array[i].address & 0xff0000) >> 16, (StoreAgent.queue.clients.array[i].address & 0xff000000) >> 24, 
                            ntohs(StoreAgent.queue.clients.array[i].port), StoreAgent.queue.clients.array[i].queue, StoreAgent.queue.clients.array[i].identifier);
                    }

                    *dataLength = ptr - data;
                } else {
                    *dataLength = count;
                }
            } else {
                if (data && (*dataLength > 27)) {
                    strcpy(data, "No agents are registered.\r\n");
                }

                *dataLength = 27;
            }

            break;
        }

        case 50: {
            unsigned char    version[30];

            PVCSRevisionToVersion(PRODUCT_VERSION, version);
            count = strlen(version) + 11;

            if (data && (*dataLength > count)) {
                ptr = data;

                PVCSRevisionToVersion(PRODUCT_VERSION, version);
                ptr += sprintf(ptr, "nmapd.c: %s\r\n", version);

                *dataLength = ptr - data;
            } else {
                *dataLength = count;
            }

            break;
        }

        case 51: {
            DMC_REPORT_PRODUCT_VERSION(data, *dataLength);
            break;
        }

        case 52: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.nmap.servicedNMAP));
            }

            *dataLength = 12;
            break;
        }

        case 53: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(StoreAgent.stats.servicedAgents));
            }

            *dataLength = 12;
            break;
        }
    }
#endif
    return(TRUE);
}

static BOOL 
WriteStoreVariable(unsigned int variable, unsigned char *data, size_t dataLength)
{
    unsigned char    *ptr;
    unsigned char    *ptr2;
    BOOL                result = TRUE;

    if (!data || !dataLength) {
        return(FALSE);
    }
#if 0
    XplRWWriteLockAcquire(&StoreAgent.lock.config);

    switch (variable) {
        case 8: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.queue.sleep = atol(data);
            if (StoreAgent.queue.sleep < 1) {
                StoreAgent.queue.sleep = 4 * 60;
            }

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 19: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                StoreAgent.quota.useUser = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                StoreAgent.quota.useUser = FALSE;
            } else {
                result = FALSE;
            }

            break;
        }

        case 20: {
            if (StoreAgent.quota.message) {
                MemFree(StoreAgent.quota.message);
                StoreAgent.quota.message = NULL;
            }

            StoreAgent.quota.message = MemStrdup(data);
            break;
        }

        case 21: {
            if (StoreAgent.quota.warning) {
                MemFree(StoreAgent.quota.warning);
                StoreAgent.quota.warning = NULL;
            }

            StoreAgent.quota.warning = MemStrdup(data);
            break;
        }

        case 25: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                StoreAgent.loadMonitor.disabled = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                StoreAgent.loadMonitor.disabled = FALSE;
            } else {
                result = FALSE;
            }

            break;
        }

        case 26: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.loadMonitor.interval = atol(data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 27: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.loadMonitor.low = atol(data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 28: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.loadMonitor.high = atol(data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 30: {
            if (StoreAgent.newShareMessage) {
                MemFree(StoreAgent.newShareMessage);
                StoreAgent.newShareMessage = NULL;
            }

            StoreAgent.newShareMessage = MemStrdup(data);
            break;
        }

        case 32: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.queue.limit.concurrent = atol(data);
            StoreAgent.queue.limit.check = CalculateCheckQueueLimit(StoreAgent.queue.limit.concurrent, StoreAgent.queue.limit.sequential);
            
            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 33: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.queue.limit.sequential = atol(data);
            StoreAgent.queue.limit.check = CalculateCheckQueueLimit(StoreAgent.queue.limit.concurrent, StoreAgent.queue.limit.sequential);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 36: {
            if (dataLength < MAXEMAILNAMESIZE) {
                StoreAgent.queue.forwardUndeliverable.enabled = TRUE;
                strcpy(StoreAgent.queue.forwardUndeliverable.address, data);
            } else {
                result = FALSE;
            }

            break;
        }

        case 37: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                StoreAgent.queue.forwardUndeliverable.enabled = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                StoreAgent.queue.forwardUndeliverable.enabled = FALSE;
            } else {
                result = FALSE;
            }

            break;
        }

        case 38: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                StoreAgent.rts.blockSpam = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                StoreAgent.rts.blockSpam = FALSE;
            } else {
                result = FALSE;
            }

            break;
        }

        case 39: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.queue.bounce.interval = atol(data);
            if (StoreAgent.queue.bounce.interval < 1) {
                StoreAgent.rts.blockSpam = FALSE;
            }

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 40: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            StoreAgent.queue.bounce.maxCount = atol(data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 41: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.nmap.toNMAP, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 42: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.storedLocal.bytes, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 43: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.storedLocal.count, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 44: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.storedLocal.recipients, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 45: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.queuedLocal.messages, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 46: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.spam, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 47: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.deliveryFailed.local, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 48: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.deliveryFailed.remote, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 52: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.nmap.servicedNMAP, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 53: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(StoreAgent.stats.servicedAgents, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 0:
        case 1:
        case 2:
        case 3:
        case 4:
        case 5:
        case 6:
        case 7:
        case 9:
        case 10:
        case 11:
        case 12:
        case 13:
        case 14:
        case 15:
        case 16:
        case 17:
        case 18:
        case 22:
        case 23:
        case 24:
        case 29:
        case 31:
        case 34:
        case 35:
        case 49:
        case 50:
        case 51:
        default: {
            result = FALSE;
            break;
        }
    }

    XplRWWriteLockRelease(&StoreAgent.lock.config);
#endif
    return(result);
}
