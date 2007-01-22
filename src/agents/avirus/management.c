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

#include "avirus.h"

#define PRODUCT_NAME "Bongo AntiVirus Agent"
#define PRODUCT_DESCRIPTION "Provides integration with external anti-virus engines."
#define PRODUCT_VERSION "$Revision: 1.00 $"

static BOOL ReadAVirusVariable(unsigned int variable, unsigned char *data, size_t *length);
static BOOL WriteAVirusVariable(unsigned int variable, unsigned char *data, size_t length);

static BOOL AVirusDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL AVirusShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL SendAntiVirusStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);

ManagementCommands AVirusManagementCommands[] = {
    { DMCMC_HELP, AVirusDMCCommandHelp }, 
    { DMCMC_SHUTDOWN, AVirusShutdown },
    { DMCMC_STATS, SendAntiVirusStatistics }, 
    { DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats },
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};

ManagementVariables AVirusManagementVariables[] = {
    { DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadAVirusVariable, NULL }, 
    { DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP, ReadAVirusVariable, NULL }, 
    { DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP, ReadAVirusVariable, NULL }, 
    { DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP, ReadAVirusVariable, NULL }, 
    { AVMV_DO_INCOMING, AVMV_DO_INCOMING_HELP, ReadAVirusVariable, WriteAVirusVariable }, 
    { AVMV_NOTIFY_SENDER, AVMV_NOTIFY_SENDER_HELP, ReadAVirusVariable, WriteAVirusVariable }, 
    { AVMV_NOTIFY_RECIPIENT, AVMV_NOTIFY_RECIPIENT_HELP, ReadAVirusVariable, WriteAVirusVariable }, 
    { DMCMV_NMAP_QUEUE, DMCMV_NMAP_QUEUE_HELP, ReadAVirusVariable, NULL }, 
    { AVMV_WORK_DIRECTORY, AVMV_WORK_DIRECTORY_HELP, ReadAVirusVariable, NULL }, 
    { AVMV_PATTERN_FILE_DIRECTORY, AVMV_PATTERN_FILE_DIRECTORY_HELP, ReadAVirusVariable, NULL }, 
    { DMCMV_NMAP_ADDRESS, DMCMV_NMAP_ADDRESS_HELP, ReadAVirusVariable, NULL }, 
    { DMCMV_OFFICIAL_NAME, DMCMV_OFFICIAL_NAME_HELP, ReadAVirusVariable, NULL }, 
    { AVMV_MESSAGES_SCANNED, AVMV_MESSAGES_SCANNED_HELP, ReadAVirusVariable, WriteAVirusVariable }, 
    { AVMV_ATTACHMENTS_SCANNED, AVMV_ATTACHMENTS_SCANNED_HELP, ReadAVirusVariable, WriteAVirusVariable }, 
    { AVMV_ATTACHMENTS_BLOCKED, AVMV_ATTACHMENTS_BLOCKED_HELP, ReadAVirusVariable, WriteAVirusVariable }, 
    { AVMV_VIRUSES_FOUND, AVMV_VIRUSES_FOUND_HELP, ReadAVirusVariable, WriteAVirusVariable }, 
    { DMCMV_VERSION, DMCMV_VERSION_HELP, ReadAVirusVariable, NULL }, 
};

ManagementVariables *
GetAVirusManagementVariables(void)
{
    return(AVirusManagementVariables);
}

int 
GetAVirusManagementVariablesCount(void)
{
    return(sizeof(AVirusManagementVariables) / sizeof(ManagementVariables));
}

ManagementCommands *
GetAVirusManagementCommands(void)
{
    return(AVirusManagementCommands);
}

int 
GetAVirusManagementCommandsCount(void)
{
    return(sizeof(AVirusManagementCommands) / sizeof(ManagementCommands));
}

static BOOL 
AVirusShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    XplThreadID id;

    if (response) {
        if (!arguments) {
            if (AVirus.nmap.conn) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
                    id = XplSetThreadGroupID(AVirus.id.group);

                    AVirus.state = AV_STATE_STOPPING;

                    if (AVirus.nmap.conn) {
                        ConnClose(AVirus.nmap.conn, 1);
                        AVirus.nmap.conn = NULL;
                    }

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }

                    XplSetThreadGroupID(id);
                }
            } else if (AVirus.state != AV_STATE_RUNNING) {
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
AVirusDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
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
SendAntiVirusStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    MemStatistics poolStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        *response = MemMalloc(sizeof(PRODUCT_NAME)
                    + sizeof(PRODUCT_SHORT_NAME)
                    + 172);

        MemPrivatePoolStatistics(AVirus.nmap.pool, &poolStats);

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
                    XplSafeRead(AVirus.server.active), 
                    XplSafeRead(AVirus.nmap.worker.active), 
                    XplSafeRead(AVirus.nmap.worker.idle), 
                    XplSafeRead(AVirus.stats.messages.scanned), 
                    XplSafeRead(AVirus.stats.attachments.scanned), 
                    XplSafeRead(AVirus.stats.attachments.blocked), 
                    XplSafeRead(AVirus.stats.viruses));

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
ReadAVirusVariable(unsigned int variable, unsigned char *data, size_t *length)
{
    size_t count;
    unsigned char *ptr;

    switch (variable) {
        case 0: {
            unsigned char version[30];

            PVCSRevisionToVersion(PRODUCT_VERSION, version);
            count = strlen(version) + 12;

            if (data && (*length > count)) {
                ptr = data;

                PVCSRevisionToVersion(PRODUCT_VERSION, version);
                ptr += sprintf(ptr, "avirus.c: %s\r\n", version);

                *length = ptr - data;
            } else {
                *length = count;
            }

            break;
        }

        case 1: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(AVirus.nmap.worker.active));
            }

            *length = 12;
            break;
        }

        case 2: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(AVirus.nmap.worker.idle));
            }

            *length = 12;
            break;
        }

        case 3: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(AVirus.server.active));
            }

            *length = 12;
            break;
        }

        case 4: {
            if ((AVirus.flags & AV_FLAG_SCAN_INCOMING) == FALSE) {
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

        case 5: {
            if ((AVirus.flags & AV_FLAG_NOTIFY_SENDER) == FALSE) {
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

        case 6: {
            if ((AVirus.flags & AV_FLAG_NOTIFY_USER) == FALSE) {
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
            if (data && (*length > 12)) {
                sprintf(data, "%010lu\r\n", AVirus.nmap.queue);
            }

            *length = 12;
            break;
        }

        case 8: {
            count = strlen(AVirus.path.work) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", AVirus.path.work);
            }

            *length = count;
            break;
        }

        case 9: {
            count = strlen(AVirus.path.patterns) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", AVirus.path.patterns);
            }

            *length = count;
            break;
        }

        case 10: {
            count = strlen(AVirus.nmap.address) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", AVirus.nmap.address);
            }

            *length = count;
            break;
        }

        case 11: {
            count = strlen(AVirus.officialName) + 2;
            if (data && (*length > count)) {
                sprintf(data, "%s\r\n", AVirus.officialName);
            }

            *length = count;
            break;
        }

        case 12: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(AVirus.stats.messages.scanned));
            }

            *length = 12;
            break;
        }

        case 13: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(AVirus.stats.attachments.scanned));
            }

            *length = 12;
            break;
        }

        case 14: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(AVirus.stats.attachments.blocked));
            }

            *length = 12;
            break;
        }

        case 15: {
            if (data && (*length > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(AVirus.stats.viruses));
            }

            *length = 12;
            break;
        }

        case 16: {
            DMC_REPORT_PRODUCT_VERSION(data, *length);
            break;
        }
    }

    return(TRUE);
}

static BOOL 
WriteAVirusVariable(unsigned int variable, unsigned char *data, size_t length)
{
    unsigned char    *ptr;
    unsigned char    *ptr2;
    BOOL                result = TRUE;

    if (!data || !length) {
        return(FALSE);
    }

    switch (variable) {
        case 4: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                AVirus.flags += AV_FLAG_SCAN_INCOMING;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                AVirus.flags &= ~AV_FLAG_SCAN_INCOMING;
            } else {
                result = FALSE;
            }

            break;
        }

        case 5: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                AVirus.flags |= AV_FLAG_NOTIFY_SENDER;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                AVirus.flags &= ~AV_FLAG_NOTIFY_SENDER;
            } else {
                result = FALSE;
            }

            break;
        }

        case 6: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                AVirus.flags |= AV_FLAG_NOTIFY_USER;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                AVirus.flags &= ~AV_FLAG_NOTIFY_USER;
            } else {
                result = FALSE;
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

            XplSafeWrite(AVirus.stats.messages.scanned, atol(data));

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

            XplSafeWrite(AVirus.stats.attachments.scanned, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 14: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(AVirus.stats.attachments.blocked, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 15: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(AVirus.stats.viruses, atol(data));

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
		case 7:
		case 8:
		case 9:
		case 10:
		case 11:
		case 16:
        default: {
            result = FALSE;
            break;
        }
    }

    return(result);
}
