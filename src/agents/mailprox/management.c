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

#include "mailprox.h"

#define PRODUCT_NAME "NetMail Proxy Agent"
#define PRODUCT_DESCRIPTION "Provides the ability for users to transfer mail from other mail systems into their NetMail account."
#define PRODUCT_VERSION "$Revision: 1.5 $"

static BOOL ReadMailProxyVariable(unsigned int variable, unsigned char *data, size_t *length);
static BOOL WriteMailProxyVariable(unsigned int variable, unsigned char *data, size_t length);

static BOOL MailProxyDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL MailProxyShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL SendMailProxyStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);

ManagementCommands MailProxyManagementCommands[] = {
    { DMCMC_HELP, MailProxyDMCCommandHelp }, 
    { DMCMC_SHUTDOWN, MailProxyShutdown },
    { DMCMC_STATS, SendMailProxyStatistics }, 
    { DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats }, 
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};

ManagementVariables MailProxyManagementVariables[] = {
    { DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP, ReadMailProxyVariable, NULL }, 
    { DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP, ReadMailProxyVariable, NULL }, 
    { DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP, ReadMailProxyVariable, NULL }, 
    { PROXMV_MAXIMUM_ACCOUNTS, PROXMV_MAXIMUM_ACCOUNTS_HELP, ReadMailProxyVariable, NULL }, 
    { PROXMV_MAXIMUM_PARALLEL_THREADS, PROXMV_MAXIMUM_PARALLEL_THREADS_HELP, ReadMailProxyVariable, NULL }, 
    { PROXMV_CONNECTION_TIMEOUT, PROXMV_CONNECTION_TIMEOUT_HELP, ReadMailProxyVariable, NULL }, 
    { PROXMV_RUN_INTERVAL, PROXMV_RUN_INTERVAL_HELP, ReadMailProxyVariable, NULL }, 
    { DMCMV_POSTMASTER, DMCMV_POSTMASTER_HELP, ReadMailProxyVariable, NULL }, 
    { DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadMailProxyVariable, NULL }, 
    { DMCMV_VERSION, DMCMV_VERSION_HELP, ReadMailProxyVariable, NULL }, 
    { DMCMV_TOTAL_CONNECTIONS, DMCMV_TOTAL_CONNECTIONS_HELP, ReadMailProxyVariable, WriteMailProxyVariable },
    { PROXYMV_REMOTE_MESSAGES_PULLED, PROXYMV_REMOTE_MESSAGES_PULLED_HELP, ReadMailProxyVariable, WriteMailProxyVariable },
    { PROXYMV_REMOTE_BYTES_PULLED, PROXYMV_REMOTE_BYTES_PULLED_HELP, ReadMailProxyVariable, WriteMailProxyVariable },
    { DMCMV_BAD_PASSWORD_COUNT, PROXYMV_REMOTE_BYTES_PULLED_HELP, ReadMailProxyVariable, WriteMailProxyVariable },
};

ManagementVariables *
GetMailProxyManagementVariables(void)
{
    return(MailProxyManagementVariables);
}

int 
GetMailProxyManagementVariablesCount(void)
{
    return(sizeof(MailProxyManagementVariables) / sizeof(ManagementVariables));
}

ManagementCommands *
GetMailProxyManagementCommands(void)
{
    return(MailProxyManagementCommands);
}

int 
GetMailProxyManagementCommandsCount(void)
{
    return(sizeof(MailProxyManagementCommands) / sizeof(ManagementCommands));
}

static BOOL 
MailProxyShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    if (response) {
        if (!arguments) {
            *response = MemStrdup("Shutting down.\r\n");
            if (*response) {
                MailProxy.state = MAILPROXY_STATE_STOPPING;

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
MailProxyDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    BOOL responded = FALSE;

    if (response) {
        if (arguments) {
            switch(toupper(arguments[0])) {
                case 'M': {
                    if (XplStrCaseCmp(arguments, DMCMC_DUMP_MEMORY_USAGE) == 0) {
                        if ((*response = MemStrdup(DMCMC_DUMP_MEMORY_USAGE_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
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
SendMailProxyStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    MemStatistics poolStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        *response = MemMalloc(sizeof(PRODUCT_NAME) 
                            + sizeof(PRODUCT_SHORT_NAME) 
                            + 172);

        MemPrivatePoolStatistics(MailProxy.client.pool, &poolStats);

        if (*response) {
            sprintf(*response, "%s (%s: v%d.%d.%d)\r\n%lu:%lu:%lu:%lu:%d:%d:%d:%d:%d:%d:%d\r\n", 
                    PRODUCT_NAME, 
                    PRODUCT_SHORT_NAME, 
                    PRODUCT_MAJOR_VERSION, 
                    PRODUCT_MINOR_VERSION, 
                    PRODUCT_LETTER_VERSION, 
                    poolStats.totalAlloc.count, 
                    poolStats.totalAlloc.size, 
                    poolStats.pitches, 
                    poolStats.strikes, 
                    XplSafeRead(MailProxy.server.active), 
                    XplSafeRead(MailProxy.client.worker.active), 
                    XplSafeRead(MailProxy.client.worker.idle), 
                    XplSafeRead(MailProxy.stats.serviced), 
                    XplSafeRead(MailProxy.stats.wrongPassword), 
                    XplSafeRead(MailProxy.stats.messages), 
                    XplSafeRead(MailProxy.stats.kiloBytes));

            return(TRUE);
        }

        if ((*response = MemStrdup("Out of memory.\r\n")) != NULL) {
            return(TRUE);
        }
    } else if ((arguments) && ((*response = MemStrdup("Arguments not allowed.\r\n")) != NULL)) {
        return(TRUE);
    }

    return(FALSE);
}

static BOOL 
ReadMailProxyVariable(unsigned int variable, unsigned char *data, size_t *length)
{
    size_t count;
    unsigned char *ptr;

    switch (variable) {
        case 0: {
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", (long unsigned int)XplSafeRead(MailProxy.server.active));
            }

            *length = 12;
            break;
        }

        case 1: {
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", (long unsigned int)XplSafeRead(MailProxy.client.worker.active));
            }

            *length = 12;
            break;
        }

        case 2: {
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", (long unsigned int)XplSafeRead(MailProxy.client.worker.idle));
            }

            *length = 12;
            break;
        }

        case 3: {
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", (long unsigned int)MailProxy.max.accounts);
            }

            *length = 12;
            break;
        }

        case 4: {
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", (long unsigned int)MailProxy.max.threadsParallel);
            }

            *length = 12;
            break;
        }

        case 5: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", CONNECTION_TIMEOUT);
            }

            *length = 12;
            break;
        }

        case 6: {
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", MailProxy.runInterval);
            }

            *length = 12;
            break;
        }

        case 7: {
            count = strlen(MailProxy.postmaster) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", MailProxy.postmaster);
            }

            *length = count;
            break;
        }

        case 8: {
            unsigned char    version[30];

            PVCSRevisionToVersion(PRODUCT_VERSION, version);
            count = strlen(version) + 11;

            if (data && (*length > count)) {
                ptr = data;

                PVCSRevisionToVersion(PRODUCT_VERSION, version);
                ptr += sprintf(ptr, "proxy.c: %s\r\n", version);

                *length = ptr - data;
            } else {
                *length = count;
            }

            break;
        }

        case 9: {
            DMC_REPORT_PRODUCT_VERSION(data, *length);
            break;
        }

        case 10: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(MailProxy.stats.serviced));
            }

            *length = 12;
            break;
        }

        case 11: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(MailProxy.stats.messages));
            }

            *length = 12;
            break;
        }

        case 12: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(MailProxy.stats.kiloBytes));
            }

            *length = 12;
            break;
        }

        case 13: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(MailProxy.stats.wrongPassword));
            }

            *length = 12;
            break;
        }
    }

    return(TRUE);
}

static BOOL 
WriteMailProxyVariable(unsigned int variable, unsigned char *data, size_t length)
{
    unsigned char    *ptr;
    unsigned char    *ptr2;
    BOOL                result = TRUE;

    if (!data || !length) {
        return(FALSE);
    }

    switch (variable) {
        case 10: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(MailProxy.stats.serviced, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 11: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(MailProxy.stats.messages, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 12: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(MailProxy.stats.kiloBytes, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 13: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(MailProxy.stats.wrongPassword, atol(data));

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
        case 8:
        case 9:
        default: {
            result = FALSE;
            break;
        }
    }

    return(result);
}
