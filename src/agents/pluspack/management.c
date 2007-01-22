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

#include "pluspack.h"

#define PRODUCT_NAME "Bongo PlusPack Agent"
#define PRODUCT_DESCRIPTION "Provides automatic copying of outgoing messages, automatic signature and access control for remote sending."
#define PRODUCT_VERSION "$Revision: 1.8 $"

static BOOL ReadPlusPackVariable(unsigned int variable, unsigned char *data, size_t *length);
static BOOL WritePlusPackVariable(unsigned int variable, unsigned char *data, size_t length);

static BOOL PlusPackDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL PlusPackShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);
static BOOL SendPlusPackStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection);

ManagementCommands PlusPackManagementCommands[] = {
    { DMCMC_HELP, PlusPackDMCCommandHelp }, 
    { DMCMC_SHUTDOWN, PlusPackShutdown },
    { DMCMC_STATS, SendPlusPackStatistics }, 
    { DMCMC_DUMP_MEMORY_USAGE, ManagementMemoryStats },
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};

ManagementVariables PlusPackManagementVariables[] = {
    { DMCMV_REVISIONS, DMCMV_REVISIONS_HELP, ReadPlusPackVariable, NULL }, 
    { DMCMV_CONNECTION_COUNT, DMCMV_CONNECTION_COUNT_HELP, ReadPlusPackVariable, NULL }, 
    { DMCMV_IDLE_CONNECTION_COUNT, DMCMV_IDLE_CONNECTION_COUNT_HELP, ReadPlusPackVariable, NULL }, 
    { DMCMV_SERVER_THREAD_COUNT, DMCMV_SERVER_THREAD_COUNT_HELP, ReadPlusPackVariable, NULL }, 
    { DMCMV_NMAP_ADDRESS, DMCMV_NMAP_ADDRESS_HELP, ReadPlusPackVariable, NULL }, 
    { DMCMV_OFFICIAL_NAME, DMCMV_OFFICIAL_NAME_HELP, ReadPlusPackVariable, NULL }, 
    { PPACKMV_SIGNATURE, PPACKMV_SIGNATURE_HELP, ReadPlusPackVariable, WritePlusPackVariable }, 
    { PPACKMV_HTML_SIGNATURE, PPACKMV_HTML_SIGNATURE_HELP, ReadPlusPackVariable, WritePlusPackVariable }, 
    { PPACKMV_BIGBROTHER_USER, PPACKMV_BIGBROTHER_USER_HELP, ReadPlusPackVariable, WritePlusPackVariable }, 
    { PPACKMV_BIGBROTHER_USE_MAILBOX, PPACKMV_BIGBROTHER_USE_MAILBOX_HELP, ReadPlusPackVariable, WritePlusPackVariable }, 
    { PPACKMV_BIGBROTHER_MAILBOX, PPACKMV_BIGBROTHER_MAILBOX_HELP, ReadPlusPackVariable, WritePlusPackVariable }, 
    { PPACKMV_BIGBROTHER_SENTBOX, PPACKMV_BIGBROTHER_SENTBOX_HELP, ReadPlusPackVariable, WritePlusPackVariable }, 
    { PPACKMV_ACL_GROUP, PPACKMV_ACL_GROUP_HELP, ReadPlusPackVariable, WritePlusPackVariable }, 
    { DMCMV_VERSION, DMCMV_VERSION_HELP, ReadPlusPackVariable, NULL }, 
};

ManagementVariables *
GetPlusPackManagementVariables(void)
{
    return(PlusPackManagementVariables);
}

int 
GetPlusPackManagementVariablesCount(void)
{
    return(sizeof(PlusPackManagementVariables) / sizeof(ManagementVariables));
}

ManagementCommands *
GetPlusPackManagementCommands(void)
{
    return(PlusPackManagementCommands);
}

int 
GetPlusPackManagementCommandsCount(void)
{
    return(sizeof(PlusPackManagementCommands) / sizeof(ManagementCommands));
}

unsigned char *
GetPlusPackVersion(void)
{
    return(PRODUCT_VERSION);
}

static BOOL 
PlusPackShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    XplThreadID id;

    if (response) {
        if (!arguments) {
            if (PlusPack.nmap.conn.queue6) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
                    id = XplSetThreadGroupID(PlusPack.id.group);

                    PlusPack.state = PLUSPACK_STATE_UNLOADING;

                    if (PlusPack.nmap.conn.queue5) {
                        ConnClose(PlusPack.nmap.conn.queue5, 1);
                        PlusPack.nmap.conn.queue5 = NULL;
                    }

                    if (PlusPack.nmap.conn.queue6) {
                        ConnClose(PlusPack.nmap.conn.queue6, 1);
                        PlusPack.nmap.conn.queue6 = NULL;
                    }

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }

                    XplSetThreadGroupID(id);
                }
            } else if (PlusPack.state != PLUSPACK_STATE_RUNNING) {
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
PlusPackDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
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
SendPlusPackStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    MemStatistics poolStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        *response = MemMalloc(sizeof(PRODUCT_NAME)
                            + sizeof(PRODUCT_SHORT_NAME)
                            + 124);

        MemPrivatePoolStatistics(PlusPack.nmap.pool, &poolStats);

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
                    XplSafeRead(PlusPack.server.active), 
                    XplSafeRead(PlusPack.nmap.queue.active), 
                    XplSafeRead(PlusPack.nmap.queue.idle));

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
ReadPlusPackVariable(unsigned int variable, unsigned char *data, size_t *length)
{
	size_t count;
	unsigned char *ptr;

	switch (variable) {
		case 0: {
			unsigned char	version[30];

			PVCSRevisionToVersion(PRODUCT_VERSION, version);
			count = strlen(version) + 13;

			if (data && (*length > count)) {
				ptr = data;

				PVCSRevisionToVersion(PRODUCT_VERSION, version);
				ptr += sprintf(ptr, "forward.c: %s\r\n", version);

				*length = ptr - data;
			} else {
				*length = count;
			}

			break;
		}

		case 1: {
			if (data && (*length > 12)) {
				sprintf(data, "%010d\r\n", XplSafeRead(PlusPack.nmap.queue.active));
			}

			*length = 12;
			break;
		}

		case 2: {
			if (data && (*length > 12)) {
				sprintf(data, "%010d\r\n", XplSafeRead(PlusPack.nmap.queue.idle));
			}

			*length = 12;
			break;
		}

		case 3: {
			if (data && (*length > 12)) {
				sprintf(data, "%010d\r\n", XplSafeRead(PlusPack.server.active));
			}

			*length = 12;
			break;
		}

		case 4: {
			count = strlen(PlusPack.nmap.address) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.nmap.address);
			}

			*length = count;
			break;
		}

		case 5: {
			count = strlen(PlusPack.officialName) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.officialName);
			}

			*length = count;
			break;
		}

        case 6: {
			count = strlen(PlusPack.signature.plain) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.signature.plain);
			}

			*length = count;
			break;
        }

        case 7: {
			count = strlen(PlusPack.signature.html) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.signature.html);
			}

			*length = count;
			break;
        }

        case 8: {
			count = strlen(PlusPack.copy.user) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.copy.user);
			}

			*length = count;
			break;
        }

        case 9: {
            if (PlusPack.copy.inbox[0]) {
                ptr = "TRUE";
            } else {
                ptr = "FALSE";
            }

    		count = strlen(ptr) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", ptr);
			}
			*length = count;
			break;
        }

        case 10: {
			count = strlen(PlusPack.copy.inbox) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.copy.inbox);
			}

			*length = count;
			break;
        }

        case 11: {
			count = strlen(PlusPack.copy.outbox) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.copy.outbox);
			}

			*length = count;
			break;
        }

        case 12: {
			count = strlen(PlusPack.allowedGroup) + 2;
			if (data && (*length > count)) {
				sprintf(data, "%s\r\n", PlusPack.allowedGroup);
			}

			*length = count;
			break;
        }

		case 13: {
			DMC_REPORT_PRODUCT_VERSION(data, *length);
			break;
		}
	}

    return(TRUE);
}

static BOOL 
WritePlusPackVariable(unsigned int variable, unsigned char *data, size_t length)
{
    BOOL result = TRUE;
	unsigned char *ptr;

    if (data && length) {
	    switch (variable) {
            case 6: {
                ptr = MemStrdup(data);
                if (ptr) {
                    if (PlusPack.signature.plain) {
                        MemFree(PlusPack.signature.plain);
                    }

                    PlusPack.signature.plain = ptr;
                }

			    break;
            }

            case 7: {
                ptr = MemStrdup(data);
                if (ptr) {
                    if (PlusPack.signature.html) {
                        MemFree(PlusPack.signature.html);
                    }

                    PlusPack.signature.html = ptr;
                }

			    break;
            }

            case 8: {
                if (length < (sizeof(PlusPack.copy.user) - 1)) {
                    strcpy(PlusPack.copy.user, data);
                }

			    break;
            }

            case 9: {
                if ((toupper(data[0]) == 'F') || (atol(data) == 0)) {
                    PlusPack.copy.inbox[0] = '\0';
                }

			    break;
            }

            case 10: {
                if (length < (sizeof(PlusPack.copy.inbox) - 1)) {
                    strcpy(PlusPack.copy.inbox, data);
                }

			    break;
            }

            case 11: {
                if (length < (sizeof(PlusPack.copy.outbox) - 1)) {
                    strcpy(PlusPack.copy.outbox, data);
                }

			    break;
            }

            case 12: {
                if (length < (sizeof(PlusPack.allowedGroup) - 1)) {
                    strcpy(PlusPack.allowedGroup, data);
                }

			    break;
            }

            case 0: 
            case 1: 
            case 2: 
            case 3: 
            case 4: 
            case 5: 
		    case 13: {
                result = FALSE;
			    break;
		    }
	    }
    }

    return(result);
}
