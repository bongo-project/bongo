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

#include "antispam.h"

#define PRODUCT_NAME "Bongo AntiSpam Agent"
#define PRODUCT_DESCRIPTION "Provides antispam disallowed domains filtering."
#define PRODUCT_VERSION "$Revision: 1.00 $"

static BOOL ReadASpamVariable(unsigned int variable, unsigned char *data, size_t *length);

static BOOL ASpamDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL ASpamShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL SendASpamStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);

ManagementCommands ASpamManagementCommands[] = {
    { DMCMC_HELP, ASpamDMCCommandHelp }, 
    { DMCMC_SHUTDOWN, ASpamShutdown },
    { DMCMC_STATS, SendASpamStatistics }, 
    { DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats }, 
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};

ManagementVariables ASpamManagementVariables[] = {
    { DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadASpamVariable, NULL }, 
    { DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP, ReadASpamVariable, NULL }, 
    { DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP, ReadASpamVariable, NULL }, 
    { DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP, ReadASpamVariable, NULL }, 
    { DMCMV_NMAP_QUEUE, DMCMV_NMAP_QUEUE_HELP, ReadASpamVariable, NULL }, 
    { DMCMV_NMAP_ADDRESS, DMCMV_NMAP_ADDRESS_HELP, ReadASpamVariable, NULL }, 
    { ASPMMV_DO_RTS, ASPMMV_DO_RTS_HELP, ReadASpamVariable, NULL }, 
    { DMCMV_NOTIFY_POSTMASTER, ASPMMV_NOTIFY_POSTMASTER, ReadASpamVariable, NULL }, 
    { DMCMV_POSTMASTER, DMCMV_POSTMASTER_HELP, ReadASpamVariable, NULL }, 
    { ASPMMV_BLACK_LIST_ARRAY, ASPMMV_BLACK_LIST_ARRAY_HELP, ReadASpamVariable, NULL }, 
    { DMCMV_VERSION, DMCMV_VERSION_HELP, ReadASpamVariable, NULL }, 
};

ManagementVariables *
GetASpamManagementVariables(void)
{
    return(ASpamManagementVariables);
}

int 
GetASpamManagementVariablesCount(void)
{
    return(sizeof(ASpamManagementVariables) / sizeof(ManagementVariables));
}

ManagementCommands *
GetASpamManagementCommands(void)
{
    return(ASpamManagementCommands);
}

int 
GetASpamManagementCommandsCount(void)
{
    return(sizeof(ASpamManagementCommands) / sizeof(ManagementCommands));
}

static BOOL 
ASpamShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    XplThreadID id;

    if (response) {
        if (!arguments) {
            if (ASpam.nmap.conn) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
                    id = XplSetThreadGroupID(ASpam.id.group);

                    ASpam.state = ASPAM_STATE_STOPPING;

                    if (ASpam.nmap.conn) {
                        ConnClose(ASpam.nmap.conn, 1);
                        ASpam.nmap.conn = NULL;
                    }

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }

                    XplSetThreadGroupID(id);
                }
            } else if (ASpam.state != ASPAM_STATE_RUNNING) {
                *response = MemStrdup("Shutdown in progress.\r\n");
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
ASpamDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
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
SendASpamStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    MemStatistics poolStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        *response = MemMalloc(sizeof(PRODUCT_NAME)
                            + sizeof(PRODUCT_SHORT_NAME)
                            + 124);

        MemPrivatePoolStatistics(ASpam.nmap.pool, &poolStats);

        if (*response) {
            sprintf(*response, "%s (%s: v%d.%d.%d)\r\n%lu:%lu:%lu:%lu:%d:%d:%d\r\n", 
                    PRODUCT_NAME, 
                    PRODUCT_SHORT_NAME, 
                    PRODUCT_MAJOR_VERSION, 
                    PRODUCT_MINOR_VERSION, 
                    PRODUCT_LETTER_VERSION, 
                    poolStats.totalAlloc.count, 
                    poolStats.totalAlloc.size, 
                    poolStats.pitches, 
                    poolStats.strikes, 
                    XplSafeRead(ASpam.server.active), 
                    XplSafeRead(ASpam.nmap.worker.active), 
                    XplSafeRead(ASpam.nmap.worker.idle));

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
ReadASpamVariable(unsigned int variable, unsigned char *data, size_t *length)
{
    unsigned long used;
    size_t count;
    unsigned char *ptr;

    switch (variable) {
        case 0: {
            unsigned char    version[30];

            PVCSRevisionToVersion(PRODUCT_VERSION, version);
            count = strlen(version) + 14;

            if (data && (*length > count)) {
                ptr = data;

                PVCSRevisionToVersion(PRODUCT_VERSION, version);
                ptr += sprintf(ptr, "antispam.c: %s\r\n", version);

                *length = ptr - data;
            } else {
                *length = count;
            }

            break;
        }

        case 1: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ASpam.nmap.worker.active));
            }

            *length = 12;
            break;
        }

        case 2: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ASpam.nmap.worker.idle));
            }

            *length = 12;
            break;
        }

        case 3: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ASpam.server.active));
            }

            *length = 12;
            break;
        }

        case 4: {
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", ASpam.nmap.queue);
            }

            *length = 12;
            break;
        }

        case 5: {
            count = strlen(ASpam.nmap.address) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", ASpam.nmap.address);
            }

            *length = count;
            break;
        }

        case 6: {
            if (!(ASpam.flags & ASPAM_FLAG_RETURN_TO_SENDER)) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*length > count)) {
                strcpy(data, ptr);
            }

            *length = count;
            break;
        }

        case 7: {
            if (!(ASpam.flags & ASPAM_FLAG_NOTIFY_POSTMASTER)) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*length > count)) {
                strcpy(data, ptr);
            }

            *length = count;
            break;
        }

        case 8: {
            count = strlen(ASpam.postmaster) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", ASpam.postmaster);
            }

            *length = count;
            break;
        }

        case 9: {
            if (ASpam.disallow.used) {
                count = 0;

                for (used = 0; used < ASpam.disallow.used; used++) {
                    count += strlen(ASpam.disallow.list->Value[used]) + 2;
                }

                if (data && (*length > count)) {
                    ptr = data;
                    for (used = 0; used < ASpam.disallow.used; used++) {
                        ptr += sprintf(ptr, "%s\r\n", ASpam.disallow.list->Value[used]);
                    }
                    *length = ptr - data;
                } else {
                    *length = count;
                }
            } else {
                if (data && (*length > 38)) {
                    strcpy(data, "No addresses have been black listed.\r\n");
                }

                *length = 38;
            }
            break;
        }

        case 10: {
            DMC_REPORT_PRODUCT_VERSION(data, *length);
            break;
        }
    }

    return(TRUE);
}
