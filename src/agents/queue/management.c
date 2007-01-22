/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001-2005 Novell, Inc. All Rights Reserved.
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

#include "queued.h"
#include "queue.h"
#include "conf.h"
#include "domain.h"

static BOOL ReadQueueVariable(unsigned int Variable, unsigned char *Data, size_t *DataLength);
#if QUEUE_COMPLETE
static BOOL WriteQueueVariable(unsigned int Variable, unsigned char *Data, size_t DataLength);
#endif
static BOOL QueueDMCShutdown(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL QueueDMCCommandHelp(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL SendQueueStatistics(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL DumpQueueData(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL RemoveQueueData(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL ListQueueAgents(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);

ManagementVariables QueueManagementVariables[] = {
    { DMCMV_MESSAGING_SERVER_DN, DMCMV_MESSAGING_SERVER_DN_HELP, ReadQueueVariable, NULL }, 
#if QUEUE_COMPLETE
    { DMCMV_HOSTNAME, DMCMV_HOSTNAME_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_POSTMASTER, DMCMV_POSTMASTER_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_OFFICIAL_NAME, DMCMV_OFFICIAL_NAME_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_QUEUE_THREAD_COUNT, DMCMV_QUEUE_THREAD_COUNT_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_QUEUE_SLEEP_INTERVAL, NMAPMV_QUEUE_SLEEP_INTERVAL_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { DMCMV_MAIL_DIRECTORY, DMCMV_MAIL_DIRECTORY_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_SCMS_DIRECTORY, DMCMV_SCMS_DIRECTORY_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_SPOOL_DIRECTORY, DMCMV_SPOOL_DIRECTORY_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_DBF_DIRECTORY, DMCMV_DBF_DIRECTORY_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_BYTES_PER_BLOCK, NMAPMV_BYTES_PER_BLOCK_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_TRUSTED_HOSTS, NMAPMV_TRUSTED_HOSTS_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_TRUSTED_HOST_COUNT, NMAPMV_TRUSTED_HOST_COUNT_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_SCMS_USER_THRESHOLD, NMAPMV_SCMS_USER_THRESHOLD_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_SCMS_SIZE_THRESHOLD, NMAPMV_SCMS_SIZE_THRESHOLD_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_USE_SYSTEM_QUOTA, NMAPMV_USE_SYSTEM_QUOTA_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_USE_USER_QUOTA, NMAPMV_USE_USER_QUOTA_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_QUOTA_MESSAGE, NMAPMV_QUOTA_MESSAGE_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_QUOTA_WARNING, NMAPMV_QUOTA_WARNING_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_MINIMUM_FREE_SPACE, NMAPMV_MINIMUM_FREE_SPACE_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_DISK_SPACE_LOW, NMAPMV_DISK_SPACE_LOW_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_DO_DEFERRED_DELIVERY, NMAPMV_DO_DEFERRED_DELIVERY_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_LOAD_MONITOR_DISABLED, NMAPMV_LOAD_MONITOR_DISABLED_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_LOAD_MONITOR_INTERVAL, NMAPMV_LOAD_MONITOR_INTERVAL_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_LOW_UTILIZATION_THRESHOLD, NMAPMV_LOW_UTILIZATION_THRESHOLD_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_HIGH_UTILIZATION_THRESHOLD, NMAPMV_HIGH_UTILIZATION_THRESHOLD_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_QUEUE_LOAD_THRESHOLD, NMAPMV_QUEUE_LOAD_THRESHOLD_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_NEW_SHARE_MESSAGE, NMAPMV_NEW_SHARE_MESSAGE_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_USER_NAMESPACE_PREFIX, NMAPMV_USER_NAMESPACE_PREFIX_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_QUEUE_LIMIT_CONCURRENT, NMAPMV_QUEUE_LIMIT_CONCURRENT_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_QUEUE_LIMIT_SEQUENTIAL, NMAPMV_QUEUE_LIMIT_SEQUENTIAL_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_RTS_HANDLING, NMAPMV_RTS_HANDLING_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_RTS_MAX_BODY_SIZE, NMAPMV_RTS_MAX_BODY_SIZE_HELP, ReadQueueVariable, NULL }, 
    { NMAPMV_FORWARD_UNDELIVERABLE_ADDR, NMAPMV_FORWARD_UNDELIVERABLE_ADDR_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_FORWARD_UNDELIVERABLE, NMAPMV_FORWARD_UNDELIVERABLE_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_BLOCK_RTS_SPAM, NMAPMV_BLOCK_RTS_SPAM_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_BOUNCE_INTERVAL, NMAPMV_BOUNCE_INTERVAL_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_MAX_BOUNCE_COUNT, NMAPMV_MAX_BOUNCE_COUNT_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_NMAP_TO_NMAP_CONNECTIONS, NMAPMV_NMAP_TO_NMAP_CONNECTIONS_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_BYTES_STORED_LOCALLY, NMAPMV_BYTES_STORED_LOCALLY_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_MESSAGES_STORED_LOCALLY, NMAPMV_MESSAGES_STORED_LOCALLY_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_RECIPIENTS_STORED_LOCALLY, NMAPMV_RECIPIENTS_STORED_LOCALLY_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_MESSAGES_QUEUED_LOCALLY, NMAPMV_MESSAGES_QUEUED_LOCALLY_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_SPAM_MESSAGES_DISCARDED, NMAPMV_SPAM_MESSAGES_DISCARDED_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_LOCAL_DELIVERY_FAILURES, NMAPMV_LOCAL_DELIVERY_FAILURES_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_REMOTE_DELIVERY_FAILURES, NMAPMV_REMOTE_DELIVERY_FAILURES_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_REGISTERED_AGENTS_ARRAY, NMAPMV_REGISTERED_AGENTS_ARRAY_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_VERSION, DMCMV_VERSION_HELP, ReadQueueVariable, NULL }, 
    { DMCMV_TOTAL_CONNECTIONS, DMCMV_TOTAL_CONNECTIONS_HELP, ReadQueueVariable, WriteQueueVariable }, 
    { NMAPMV_AGENTS_SERVICED, NMAPMV_AGENTS_SERVICED_HELP, ReadQueueVariable, WriteQueueVariable }, 
#endif
};

ManagementCommands QueueManagementCommands[] = {
    { DMCMC_HELP, QueueDMCCommandHelp }, 
    { DMCMC_SHUTDOWN, QueueDMCShutdown },
    { DMCMC_STATS, SendQueueStatistics }, 
    { DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats }, 
    { "QDump", DumpQueueData }, 
    { "QRemove", RemoveQueueData }, 
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
    { "ListAgents", ListQueueAgents },
};

static ManagementVariables *
GetQueueManagementVariables(void)
{
    return(QueueManagementVariables);
}

static int 
GetQueueManagementVariablesCount(void)
{
    return(sizeof(QueueManagementVariables) / sizeof(ManagementVariables));
}

static ManagementCommands *
GetQueueManagementCommands(void)
{
    return(QueueManagementCommands);
}

static int 
GetQueueManagementCommandsCount(void)
{
    return(sizeof(QueueManagementCommands) / sizeof(ManagementCommands));
}

static BOOL 
QueueDMCShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    if (response) {
        if (!arguments) {
            if (Agent.clientListener) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
                    Agent.agent.state = BONGO_AGENT_STATE_UNLOADING;

                    ConnClose(Agent.clientListener, 1);
                    Agent.clientListener = NULL;

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }
                }
            } else if (Agent.agent.state == BONGO_AGENT_STATE_UNLOADING) {
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
QueueDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    BOOL responded = FALSE;

    if (response) {
        if (arguments) {
            switch(toupper(arguments[0])) {
                case 'Q': {
                    if (XplStrCaseCmp(arguments, "QDump") == 0) {
                        if ((*response = MemStrdup("Store an queued messages summary in the file queue.ims; in the configured DBF directory.\r\n")) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    } else if (XplStrCaseCmp(arguments, "QRemove") == 0) {
                        if ((*response = MemStrdup("Remove all queued messages for the specified domain.  The QRemove command and the domain to be removed should be seperated with a single space.\r\n")) != NULL) {
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
SendQueueStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
#if QUEUE_COMPLETE
    MemStatistics poolStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        *response = MemMalloc(sizeof(PRODUCT_NAME) /* Long Name */
                        + sizeof(PRODUCT_SHORT_NAME) /* Short Name */
                        + 10 /* PRODUCT_MAJOR_VERSION */
                        + 10 /* PRODUCT_MINOR_VERSION */
                        + 10 /* PRODUCT_LETTER_VERSION */
                        + 10 /* Connection Pool Allocation Count */
                        + 10 /* Connection Pool Memory Usage */
                        + 10 /* Connection Pool Pitches */
                        + 10 /* Connection Pool Strikes */
                        + 10 /* DMCMV_SERVER_THREAD_COUNT */
                        + 10 /* DMCMV_CONNECTION_COUNT */
                        + 10 /* DMCMV_IDLE_CONNECTION_COUNT */
                        + 10 /* DMCMV_QUEUE_THREAD_COUNT */
                        + 5 /* NMAPMV_LOAD_MONITOR_DISABLED */
                        + 10 /* NMAPMV_LOAD_MONITOR_INTERVAL */
                        + 10 /* NMAPMV_LOW_UTILIZATION_THRESHOLD */
                        + 10 /* NMAPMV_HIGH_UTILIZATION_THRESHOLD */
                        + 10 /* NMAPMV_QUEUE_LIMIT_CONCURRENT */
                        + 10 /* NMAPMV_QUEUE_LIMIT_SEQUENTIAL */
                        + 10 /* NMAPMV_QUEUE_LOAD_THRESHOLD */
                        + 10 /* NMAPMV_BOUNCE_INTERVAL */
                        + 10 /* NMAPMV_MAX_BOUNCE_COUNT */
                        + 10 /* NMAPMV_NMAP_TO_NMAP_CONNECTIONS */
                        + 10 /* DMCMV_TOTAL_CONNECTIONS */
                        + 10 /* NMAPMV_AGENTS_SERVICED */
                        + 10 /* NMAPMV_MESSAGES_QUEUED_LOCALLY */
                        + 10 /* NMAPMV_SPAM_MESSAGES_DISCARDED */
                        + 10 /* NMAPMV_MESSAGES_STORED_LOCALLY */
                        + 10 /* NMAPMV_RECIPIENTS_STORED_LOCALLY */
                        + 10 /* NMAPMV_BYTES_STORED_LOCALLY */
                        + 10 /* NMAPMV_LOCAL_DELIVERY_FAILURES */
                        + 10 /* NMAPMV_REMOTE_DELIVERY_FAILURES */
                        + 64); /* Formatting */

        MemPrivatePoolStatistics(NMAP.client.pool, &poolStats);

        if (*response) {
            sprintf(*response, "%s (%s: v%d.%d.%d)\r\n%lu:%lu:%lu:%lu:%d:%d:%d:%d:%d:%lu:%lu:%lu:%lu:%ld:%ld:%ld:%lu:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d\r\n", 
                    PRODUCT_NAME, 
                    PRODUCT_SHORT_NAME, 
                    PRODUCT_MAJOR_VERSION, 
                    PRODUCT_MINOR_VERSION, 
                    PRODUCT_LETTER_VERSION, 
                    poolStats.totalAlloc.count, 
                    poolStats.totalAlloc.size, 
                    poolStats.pitches, 
                    poolStats.strikes, 
                    XplSafeRead(NMAP.server.active), 
                    XplSafeRead(NMAP.client.worker.active), 
                    XplSafeRead(NMAP.client.worker.idle), 
                    XplSafeRead(NMAP.queue.worker.active), 
                    NMAP.loadMonitor.disabled, 
                    NMAP.loadMonitor.interval, 
                    NMAP.loadMonitor.low, 
                    NMAP.loadMonitor.high, 
                    NMAP.queue.limit.concurrent, 
                    NMAP.queue.limit.sequential, 
                    NMAP.queue.limit.trigger, 
                    NMAP.queue.bounce.interval, 
                    NMAP.queue.bounce.maxCount, 
                    XplSafeRead(NMAP.stats.nmap.toNMAP), 
                    XplSafeRead(NMAP.stats.nmap.servicedNMAP), 
                    XplSafeRead(NMAP.stats.servicedAgents), 
                    XplSafeRead(NMAP.stats.queuedLocal.messages), 
                    XplSafeRead(NMAP.stats.spam), 
                    XplSafeRead(NMAP.stats.storedLocal.count), 
                    XplSafeRead(NMAP.stats.storedLocal.recipients), 
                    XplSafeRead(NMAP.stats.storedLocal.bytes), 
                    XplSafeRead(NMAP.stats.deliveryFailed.local), 
                    XplSafeRead(NMAP.stats.deliveryFailed.remote));

            return(TRUE);
        }

        if ((*response = MemStrdup("Out of memory.\r\n")) != NULL) {
            return(TRUE);
        }
    } else if ((arguments) && ((*response = MemStrdup("arguments not allowed.\r\n")) != NULL)) {
        return(TRUE);
    }

#endif
    return(FALSE);
}

static BOOL 
DumpQueueData(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    unsigned char buffer[XPL_MAX_PATH + 1];
    struct stat sb;
    FILE *summary;

    if (response) {
        if (XplSafeRead(Queue.queuedLocal)) {
            if (!arguments) {
                QDBSummarizeQueue();

                MsgGetDBFDir(buffer);
                strcat(buffer, "/queue.ims");

                summary = fopen(buffer, "rb");
                if (summary) {
                    stat(buffer, &sb);

                    *response = MemMalloc(sb.st_size + 3);
                    if (*response) {
                        fread(*response, sizeof(unsigned char), sb.st_size, summary);
                        (*response)[sb.st_size - 2] = '\r';
                        (*response)[sb.st_size - 1] = '\n';
                        (*response)[sb.st_size] = '\0';
                    }

                    fclose(summary);
                } else {
                    *response = MemStrdup("No .\r\n");
                }
            } else {
                *response = MemStrdup("arguments not allowed.\r\n");
            }
        } else {
            *response = MemStrdup("No queue entries.\r\n");
        }

        if (*response) {
            return(TRUE);
        }
    }

    return(FALSE);
}

static BOOL
RemoveQueueData(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    if (response) {
        if (arguments) {
            unsigned char *ptr;

            ptr = strchr(arguments, 0x0A);
            if (ptr) {
                *ptr = '\0';
            }

            ptr = strrchr(arguments, 0x0D);
            if (ptr) {
                *ptr = '\0';
            }

            QDBDump(arguments);

            *response = MemStrdup("Done.\r\n");
        } else {
            *response = MemStrdup("Specify the domain to be removed.\r\n");
        }

        if (*response) {
            return(TRUE);
        }
    }

    return(FALSE);
}

static BOOL
ListQueueAgents(unsigned char *arguments, unsigned char **responseOut, BOOL *closeConnection)
{
    int i;
    char *response;
    char *ptr;
    int ccode;
    int numBytes;
    
    numBytes = Queue.pushClients.count * CONN_BUFSIZE;
    ptr = response = MemMalloc(numBytes);
    
    ccode = 0;
    for (i = 0; numBytes > 0 && (i < Queue.pushClients.count); i++) {
        int read;
        
        read = snprintf(ptr, numBytes, "2001-Queue client:%ld.%ld.%ld.%ld Port:%d Queue:%d (ID:%s)\r\n",
                        Queue.pushClients.array[i].address & 0xff, 
                        (Queue.pushClients.array[i].address & 0xff00) >> 8, 
                        (Queue.pushClients.array[i].address & 0xff0000) >> 16, 
                        (Queue.pushClients.array[i].address & 0xff000000) >> 24, 
                        ntohs(Queue.pushClients.array[i].port), 
                        Queue.pushClients.array[i].queue, 
                        Queue.pushClients.array[i].identifier);
        numBytes -= read;
        ptr += read;
    }
    
    *responseOut = response;

    return TRUE;
}

static BOOL 
ReadQueueVariable(unsigned int variable, unsigned char *data, size_t *dataLength)
{
    size_t count;

    switch (variable) {
    case 0: {
        count = strlen(MsgGetServerDN(NULL)) + 2;
        if (data && (*dataLength > count)) {
            sprintf(data, "%s\r\n", MsgGetServerDN(NULL));
        }
        
        *dataLength = count;
        break;
    }
    default :
        return FALSE;
    }
    
    return TRUE;
    
#if QUEUE_COMPLETE
        case 1: {
            count = strlen(NMAP.server.host) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.server.host);
            }

            *dataLength = count;
            break;
        }

        case 2: {
            count = strlen(NMAP.postMaster) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.postMaster);
            }

            *dataLength = count;
            break;
        }

        case 3: {
            count = strlen(NMAP.officialName) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.officialName);
            }

            *dataLength = count;
            break;
        }

        case 4: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.server.active));
            }

            *dataLength = 12;
            break;
        }

        case 5: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.client.worker.active));
            }

            *dataLength = 12;
            break;
        }

        case 6: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.client.worker.idle));
            }

            *dataLength = 12;
            break;
        }

        case 7: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.queue.worker.active));
            }

            *dataLength = 12;
            break;
        }

        case 8: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.queue.sleep);
            }

            *dataLength = 12;
            break;
        }

        case 9: {
            count = strlen(NMAP.path.mail) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.path.mail);
            }

            *dataLength = count;
            break;
        }

        case 10: {
            count = strlen(NMAP.path.scms) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.path.scms);
            }

            *dataLength = count;
            break;
        }

        case 11: {
            count = strlen(NMAP.path.spool) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.path.spool);
            }

            *dataLength = count;
            break;
        }

        case 12: {
            count = strlen(NMAP.path.dbf) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.path.dbf);
            }

            *dataLength = count;
            break;
        }

        case 13: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.server.bytesPerBlock);
            }

            *dataLength = 12;
            break;
        }

        case 14: {
            XplRWReadLockAcquire(&NMAP.lock.config);

            if (NMAP.trusted.count) {
                if (data && (*dataLength > (size_t)(NMAP.trusted.count * 17))) {
                    ptr = data;
                    for (index = 0; index < NMAP.trusted.count; index++) {
                        ptr += sprintf(ptr, "%ld.%ld.%ld.%ld\r\n", NMAP.trusted.hosts[index] & 0xFF, (NMAP.trusted.hosts[index] & 0xFF00) >> 8, (NMAP.trusted.hosts[index] & 0xFF0000) >> 16, (NMAP.trusted.hosts[index] & 0xFF000000) >> 24);
                    }

                    *dataLength = ptr - data;
                } else {
                    *dataLength = NMAP.trusted.count * 17;
                }
            } else {
                if (data && (*dataLength > 40)) {
                    strcpy(data, "No trusted hosts have been configured.\r\n");
                }
                *dataLength = 40;
            }

            XplRWReadLockRelease(&NMAP.lock.config);

            break;
        }

        case 15: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.trusted.count);
            }

            *dataLength = 12;
            break;
        }

        case 16: {
            if (data && (*dataLength >= 12)) {
                sprintf(data, "%010d\r\n", NMAP.scms.userThreshold);
            }

            *dataLength = 12;
            break;
        }

        case 17: {
            if (data && (*dataLength >= 12)) {
                sprintf(data, "%010lu\r\n", NMAP.scms.sizeThreshold);
            }

            *dataLength = 12;
            break;
        }

        case 18: {
            if (NMAP.quota.useSystem == FALSE) {
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
            if (NMAP.quota.useUser == FALSE) {
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
            if (NMAP.quota.message && NMAP.quota.message[0]) {
                count = strlen(NMAP.quota.message) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", NMAP.quota.message);
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
            if (NMAP.quota.warning) {
                count = strlen(NMAP.quota.warning) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", NMAP.quota.warning);
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
                sprintf(data, "%010lu\r\n", NMAP.queue.minimumFree);
            }

            *dataLength = 12;
            break;
        }

        case 23: {
            if (!(NMAP.flags & NMAP_FLAG_DISK_SPACE_LOW)) {
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
            if (!NMAP.defer.enabled) {
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
            if (NMAP.loadMonitor.disabled == FALSE) {
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
                sprintf(data, "%010lu\r\n", NMAP.loadMonitor.interval);
            }

            *dataLength = 12;
            break;
        }

        case 27: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.loadMonitor.low);
            }

            *dataLength = 12;
            break;
        }

        case 28: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.loadMonitor.high);
            }

            *dataLength = 12;
            break;
        }

        case 29: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010ld\r\n", NMAP.queue.limit.trigger);
            }

            *dataLength = 12;
            break;
        }

        case 30: {
            count = strlen(NMAP.newShareMessage) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.newShareMessage);
            }

            *dataLength = count;
            break;
        }

        case 31: {
            count = strlen(NMAP.nameSpace.userPrefix) + 2;
            if (data && (*dataLength > count)) {
                sprintf(data, "%s\r\n", NMAP.nameSpace.userPrefix);
            }

            *dataLength = count;
            break;
        }

        case 32: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010ld\r\n", NMAP.queue.limit.concurrent);
            }

            *dataLength = 12;
            break;
        }

        case 33: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010ld\r\n", NMAP.queue.limit.sequential);
            }

            *dataLength = 12;
            break;
        }

        case 34: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.rts.handling);
            }

            *dataLength = 12;
            break;
        }

        case 35: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.rts.maxBodySize);
            }

            *dataLength = 12;
            break;
        }

        case 36: {
            if (NMAP.queue.forwardUndeliverable.enabled) {
                count = strlen(NMAP.queue.forwardUndeliverable.address) + 2;
                if (data && (*dataLength > count)) {
                    sprintf(data, "%s\r\n", NMAP.queue.forwardUndeliverable.address);
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
            if (NMAP.queue.forwardUndeliverable.enabled == FALSE) {
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
            if (NMAP.rts.blockSpam == FALSE) {
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
                sprintf(data, "%010lu\r\n", NMAP.queue.bounce.interval);
            }

            *dataLength = 12;
            break;
        }

        case 40: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010lu\r\n", NMAP.queue.bounce.maxCount);
            }

            *dataLength = 12;
            break;
        }

        case 41: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.nmap.toNMAP));
            }

            *dataLength = 12;
            break;
        }

        case 42: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.storedLocal.bytes));
            }

            *dataLength = 12;
            break;
        }

        case 43: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.storedLocal.count));
            }

            *dataLength = 12;
            break;
        }

        case 44: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.storedLocal.recipients));
            }

            *dataLength = 12;
            break;
        }

        case 45: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.queuedLocal.messages));
            }

            *dataLength = 12;
            break;
        }

        case 46: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.spam));
            }

            *dataLength = 12;
            break;
        }

        case 47: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.deliveryFailed.local));
            }

            *dataLength = 12;
            break;
        }

        case 48: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.deliveryFailed.remote));
            }

            *dataLength = 12;
            break;
        }

        case 49: {
            if (NMAP.queue.clients.count) {
                unsigned long i;

                count = 0;
                for (i = 0; i < NMAP.queue.clients.count; i++) {
                    count += (15 + 3 + 3 + strlen(NMAP.queue.clients.array[i].identifier) + 28 + 1);
                }

                if (data && (*dataLength > count)) {
                    ptr = data;

                    for (i = 0; i < NMAP.queue.clients.count; i++) {
                        ptr += sprintf(ptr, "%ld.%ld.%ld.%ld Port %03d; Queue: %03d; Agent: \"%s\"\r\n", 
                            NMAP.queue.clients.array[i].address & 0xff, (NMAP.queue.clients.array[i].address & 0xff00) >> 8, (NMAP.queue.clients.array[i].address & 0xff0000) >> 16, (NMAP.queue.clients.array[i].address & 0xff000000) >> 24, 
                            ntohs(NMAP.queue.clients.array[i].port), NMAP.queue.clients.array[i].queue, NMAP.queue.clients.array[i].identifier);
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
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.nmap.servicedNMAP));
            }

            *dataLength = 12;
            break;
        }

        case 53: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(NMAP.stats.servicedAgents));
            }

            *dataLength = 12;
            break;
        }
    }

    return(TRUE);
#endif
}
#if QUEUE_COMPLETE

static BOOL 
WriteQueueVariable(unsigned int variable, unsigned char *data, size_t dataLength)
{
    unsigned char    *ptr;
    unsigned char    *ptr2;
    BOOL                result = TRUE;

    if (!data || !dataLength) {
        return(FALSE);
    }

    XplRWWriteLockAcquire(&NMAP.lock.config);

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

            NMAP.queue.sleep = atol(data);
            if (NMAP.queue.sleep < 1) {
                NMAP.queue.sleep = 4 * 60;
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
                NMAP.quota.useUser = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                NMAP.quota.useUser = FALSE;
            } else {
                result = FALSE;
            }

            break;
        }

        case 20: {
            if (NMAP.quota.message) {
                MemFree(NMAP.quota.message);
                NMAP.quota.message = NULL;
            }

            NMAP.quota.message = MemStrdup(data);
            break;
        }

        case 21: {
            if (NMAP.quota.warning) {
                MemFree(NMAP.quota.warning);
                NMAP.quota.warning = NULL;
            }

            NMAP.quota.warning = MemStrdup(data);
            break;
        }

        case 25: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                NMAP.loadMonitor.disabled = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                NMAP.loadMonitor.disabled = FALSE;
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

            NMAP.loadMonitor.interval = atol(data);

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

            NMAP.loadMonitor.low = atol(data);

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

            NMAP.loadMonitor.high = atol(data);

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 30: {
            if (NMAP.newShareMessage) {
                MemFree(NMAP.newShareMessage);
                NMAP.newShareMessage = NULL;
            }

            NMAP.newShareMessage = MemStrdup(data);
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

            NMAP.queue.limit.concurrent = atol(data);
            NMAP.queue.limit.check = CalculateCheckQueueLimit(NMAP.queue.limit.concurrent, NMAP.queue.limit.sequential);
            
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

            NMAP.queue.limit.sequential = atol(data);
            NMAP.queue.limit.check = CalculateCheckQueueLimit(NMAP.queue.limit.concurrent, NMAP.queue.limit.sequential);

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
                NMAP.queue.forwardUndeliverable.enabled = TRUE;
                strcpy(NMAP.queue.forwardUndeliverable.address, data);
            } else {
                result = FALSE;
            }

            break;
        }

        case 37: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                NMAP.queue.forwardUndeliverable.enabled = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                NMAP.queue.forwardUndeliverable.enabled = FALSE;
            } else {
                result = FALSE;
            }

            break;
        }

        case 38: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                NMAP.rts.blockSpam = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                NMAP.rts.blockSpam = FALSE;
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

            NMAP.queue.bounce.interval = atol(data);
            if (NMAP.queue.bounce.interval < 1) {
                NMAP.rts.blockSpam = FALSE;
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

            NMAP.queue.bounce.maxCount = atol(data);

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

            XplSafeWrite(NMAP.stats.nmap.toNMAP, atol(data));

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

            XplSafeWrite(NMAP.stats.storedLocal.bytes, atol(data));

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

            XplSafeWrite(NMAP.stats.storedLocal.count, atol(data));

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

            XplSafeWrite(NMAP.stats.storedLocal.recipients, atol(data));

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

            XplSafeWrite(NMAP.stats.queuedLocal.messages, atol(data));

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

            XplSafeWrite(NMAP.stats.spam, atol(data));

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

            XplSafeWrite(NMAP.stats.deliveryFailed.local, atol(data));

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

            XplSafeWrite(NMAP.stats.deliveryFailed.remote, atol(data));

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

            XplSafeWrite(NMAP.stats.nmap.servicedNMAP, atol(data));

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

            XplSafeWrite(NMAP.stats.servicedAgents, atol(data));

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

    XplRWWriteLockRelease(&NMAP.lock.config);

    return(result);
}

#endif

int
QueueManagementStart(void) 
{
    int ccode;
    XplThreadID id;
    
    if ((ManagementInit(MSGSRV_AGENT_QUEUE, Agent.agent.directoryHandle)) 
        && (ManagementSetVariables(GetQueueManagementVariables(), GetQueueManagementVariablesCount())) 
        && (ManagementSetCommands(GetQueueManagementCommands(), GetQueueManagementCommandsCount()))) {
        XplBeginThread(&id, ManagementServer, DMC_MANAGEMENT_STACKSIZE, NULL, ccode);
    } else {
        XplConsolePrintf(AGENT_NAME ": Unable to startup the management interface.\r\n");
        ccode = -1;
    }

    return ccode;
}

void
QueueManagementShutdown(void)
{
    int i;
    
    ManagementShutdown();

    for (i = 0; (ManagementState() != MANAGEMENT_STOPPED) && (i < 60); i++) {
        XplDelay(1000);
    }    
}
