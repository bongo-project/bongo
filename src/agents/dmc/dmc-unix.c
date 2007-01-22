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
#include <sys/wait.h>
#include <config.h>

#include <xpl.h>
#include <connio.h>
#include <bongoutil.h>
#include <msgapi.h>
#include <mdb.h>
#include <management.h>

#include "dmcp.h"

typedef struct tagDMCAgentData {
    pid_t pid;
    unsigned char filename[XPL_MAX_PATH + 1];
} DMCAgentData;

static pid_t leaderpid = 0;

void 
DMCSignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (DMC.state < DMC_UNLOADING) {
                DMC.state = DMC_UNLOADING;
            }

            break;
        }

        case SIGINT :
        case SIGTERM: {
            if (DMC.state == DMC_STOPPING) {
                XplUnloadApp(getpid());
            } else if (DMC.state < DMC_STOPPING) {
                DMC.state = DMC_STOPPING;
            }

            break;
        }

        case SIGCHLD:
        {
            pid_t pid;
            int stat;

            while ((pid = waitpid(-1, &stat, WNOHANG)) > 0) {
                DMCAgent *pAgent = DMC.agents.head;

                while (pAgent) {
                    if (((DMCAgentData *)(pAgent->data))->pid != pid) {
                        pAgent = pAgent->next;
                    }
                    else {
                        pAgent->flags &= ~DMC_FLAG_MODULE_LOADED;
                        break;
                    }
                }
            }
            break;
        }

        default: {
            break;
        }
    }

    return;
}

BOOL 
DMCAgentPrep(DMCAgent *Agent)
{
    unsigned char *ptr;

    if (Agent) {
        if (!Agent->data) {
            Agent->data = (void *)MemMalloc(sizeof(DMCAgentData));
        }
        if (Agent->data) {
            ((DMCAgentData *)(Agent->data))->pid = 0;
            sprintf(
                    ((DMCAgentData *)(Agent->data))->filename,
                    "%s/%s",
                    XPL_DEFAULT_BIN_DIR,
                    Agent->name);
            ptr = ((DMCAgentData *)(Agent->data))->filename +
                strlen(XPL_DEFAULT_BIN_DIR) + 1;
            while (*ptr != '\0') {
                if (*ptr != '.') {
                    *ptr = tolower(*ptr);
                } else if (XplStrCaseCmp(ptr, ".NLM") == 0) {
                    *ptr = '\0';
                   break;
                }
                ptr++;
            }
            return(TRUE);
        }
    }
    return(FALSE);
}

BOOL 
DMCStartAgent(DMCAgent *Agent)
{
    pid_t pid;
    time_t loadTime;
    BOOL result = FALSE;
    unsigned char *ptr;

    if (Agent) {
        loadTime = Agent->load;
        XplSetEffectiveUserId(0);
        pid = fork();
        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf(
                    "bongomanager: Could not drop to unprivileged user '%s', exiting.\n",
                    MsgGetUnprivilegedUser());
            DMCUnload();
            exit(1);
        }
        if (pid == 0) {                 /* child */
            if (leaderpid == 0) {
                setpgid(0, 0);
            } else {
                setpgid(0, leaderpid);
            }
            
            if ((loadTime) &&
                    ((loadTime + DMC.times.reload) > time(NULL))) {
                XplConsolePrintf(
                        "Agent %s has already been started in the last %d seconds.  Waiting to load again.\n",
                        Agent->name,
                        DMC.times.reload);
                XplDelay((loadTime + DMC.times.reload) - time(NULL));
            }
            ptr = strrchr(((DMCAgentData *)(Agent->data))->filename, '/');
            XplConsolePrintf("  loading %s\r\n", ptr + 1);
            execl(
                    ((DMCAgentData *)(Agent->data))->filename,
                    ((DMCAgentData *)(Agent->data))->filename,
                    NULL);
            perror("agent execl");	
            exit(-1);	
        } else if (pid > 0) {
            Agent->load = time(NULL);
            ((DMCAgentData *)(Agent->data))->pid = pid;
            if (leaderpid == 0) {
                leaderpid = pid;
            }
            
            result = TRUE;
        } else {
            perror("fork");
            result = FALSE;
        }
    }
    return(result);
}

void 
DMCAgentRelease(DMCAgent *Agent)
{
    if (Agent) {
        MemFree(Agent->data);
        Agent->data = NULL;
    }
    return;
}

void
DMCKillAgents(void)
{
    DMCAgent *pAgent = DMC.agents.head;

    XplSetEffectiveUserId(0);
    while (pAgent) {
        kill(((DMCAgentData *)(pAgent->data))->pid, SIGKILL);
        pAgent = pAgent->next;
    }
}

