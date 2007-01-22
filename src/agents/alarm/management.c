/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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

#include "alarm.h"

#define PRODUCT_NAME "Bongo Alarm Agent"
#define PRODUCT_SHORT_NAME AGENT_NAME ".NLM"
#define PRODUCT_DESCRIPTION "Calendar Alarm Agent."
#define PRODUCT_VERSION "$Revision: 1.11 $"

static BOOL ReadAlarmVariable(unsigned int variable, unsigned char *data, size_t *length);

/* Take out the #if 0 if you need to write to a management variable */
#if 0
static BOOL WriteAlarmVariable(unsigned int variable, unsigned char *data, size_t length);
#endif

static BOOL AlarmDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL AlarmShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL SendAlarmStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);

ManagementCommands AlarmManagementCommands[] = {
    { DMCMC_HELP, AlarmDMCCommandHelp }, 
    { DMCMC_SHUTDOWN, AlarmShutdown },
    { DMCMC_STATS, SendAlarmStatistics }, 
    { DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats },
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};

ManagementVariables AlarmManagementVariables[] = {
    { DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadAlarmVariable, NULL }, 
    { DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP, ReadAlarmVariable, NULL }, 
    { DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP, ReadAlarmVariable, NULL }, 
    { DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP, ReadAlarmVariable, NULL }, 
    { DMCMV_OFFICIAL_NAME, DMCMV_OFFICIAL_NAME_HELP, ReadAlarmVariable, NULL }, 
    { DMCMV_VERSION, DMCMV_VERSION_HELP, ReadAlarmVariable, NULL }, 
};

static ManagementVariables *
GetAlarmManagementVariables(void)
{
    return(AlarmManagementVariables);
}

static int 
GetAlarmManagementVariablesCount(void)
{
    return(sizeof(AlarmManagementVariables) / sizeof(ManagementVariables));
}

static ManagementCommands *
GetAlarmManagementCommands(void)
{
    return(AlarmManagementCommands);
}

static int 
GetAlarmManagementCommandsCount(void)
{
    return(sizeof(AlarmManagementCommands) / sizeof(ManagementCommands));
}

static BOOL 
AlarmShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    if (response) {
        if (!arguments) {
            if (Alarm.clientListener) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
                    Alarm.agent.state = BONGO_AGENT_STATE_UNLOADING;

                    if (Alarm.clientListener) {
                        ConnClose(Alarm.clientListener, 1);
                        Alarm.clientListener = NULL;
                    }

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }
                }
            } else if (Alarm.agent.state != BONGO_AGENT_STATE_RUNNING) {
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
AlarmDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
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
SendAlarmStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    MemStatistics poolStats;
    BongoThreadPoolStatistics clientThreadPoolStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        *response = MemMalloc(sizeof(PRODUCT_NAME)
                    + sizeof(PRODUCT_SHORT_NAME)
                    + 172);

        MemPrivatePoolStatistics(Alarm.clientMemPool, &poolStats);
        BongoThreadPoolGetStatistics(Alarm.clientThreadPool, &clientThreadPoolStats);

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
                    1,
                    clientThreadPoolStats.total,
                    (clientThreadPoolStats.total - clientThreadPoolStats.queueLength));

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
ReadAlarmVariable(unsigned int variable, unsigned char *data, size_t *length)
{
    size_t count;
    unsigned char *ptr;
    BongoThreadPoolStatistics clientThreadPoolStats;

    BongoThreadPoolGetStatistics(Alarm.clientThreadPool, &clientThreadPoolStats);

    switch (variable) {
        case 0: {
            unsigned char version[30];

            PVCSRevisionToVersion(PRODUCT_VERSION, version);
            count = strlen(version) + 12;

            if (data && (*length > count)) {
                ptr = data;

                PVCSRevisionToVersion(PRODUCT_VERSION, version);
                ptr += sprintf(ptr, "generic.c: %s\r\n", version);

                *length = ptr - data;
            } else {
                *length = count;
            }

            break;
        }

        case 1: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", clientThreadPoolStats.total);
            }

            *length = 12;
            break;
        }

        case 2: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", clientThreadPoolStats.queueLength);
            }

            *length = 12;
            break;
        }

        case 3: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", 1);
            }

            *length = 12;
            break;
        }

        case 4: {
            count = strlen(Alarm.agent.officialName) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", Alarm.agent.officialName);
            }

            *length = count;
            break;
        }

        case 5: {
            DMC_REPORT_PRODUCT_VERSION(data, *length);
            break;
        }
    }

    return(TRUE);
}

/* Take out the #if 0 if you need to write to a management variable */
#if 0
static BOOL 
WriteAlarmVariable(unsigned int variable, unsigned char *data, size_t length)
{
    BOOL result = TRUE;

    if (!data || !length) {
        return(FALSE);
    }

    switch (variable) {
		case 0:
		case 1:
		case 2:
		case 3:
		case 4:
		case 5:
		case 6:
		case 7:
        default: {
            result = FALSE;
            break;
        }
    }

    return(result);
}
#endif

int
AlarmManagementStart(void) 
{
    int ccode;
    XplThreadID id;
    
    if ((ManagementInit(AGENT_DN, Alarm.agent.directoryHandle)) 
        && (ManagementSetVariables(GetAlarmManagementVariables(), GetAlarmManagementVariablesCount())) 
        && (ManagementSetCommands(GetAlarmManagementCommands(), GetAlarmManagementCommandsCount()))) {
        XplBeginThread(&id, ManagementServer, DMC_MANAGEMENT_STACKSIZE, NULL, ccode);
    } else {
        XplConsolePrintf(AGENT_NAME ": Unable to startup the management interface.\r\n");
        ccode = -1;
    }

    return ccode;
}

void
AlarmManagementShutdown(void)
{
    int i;
    
    ManagementShutdown();

    for (i = 0; (ManagementState() != MANAGEMENT_STOPPED) && (i < 60); i++) {
        XplDelay(1000);
    }    
}


