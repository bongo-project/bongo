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
#include <bongomanagee.h>
#include <bongoagent.h>

#include "rules.h"

#define PRODUCT_NAME "Bongo Rules Agent"
#define PRODUCT_DESCRIPTION "Provide processing of user-created message rules."
#define PRODUCT_VERSION "$Revision: 1.8 $"

ManagementCommands RulesManagementCommands[] = {
    { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};

static BongoJsonNode *
RulesReadFlag (BongoManagee *managee, BongoManageeVariable *variable)
{
    unsigned int mask = XPL_PTR_TO_INT (variable->userData);
    return BongoJsonNodeNewBool ((Rules.flags & mask) == mask);
}

void 
RulesStartManagement (void) 
{
    BongoManageeVariable vars[] = { 
        { -1, DMCMV_SERVER_THREAD_COUNT,
          BongoManageeReadAtomic, NULL, &Rules.server.active }, 
        { -1, DMCMV_CONNECTION_COUNT, 
          BongoManageeReadAtomic, NULL, &Rules.nmap.worker.active }, 
        { -1, DMCMV_IDLE_CONNECTION_COUNT, 
          BongoManageeReadAtomic, NULL, &Rules.nmap.worker.idle }, 
        { -1, DMCMV_NMAP_ADDRESS, 
          BongoManageeReadString, NULL, Rules.nmap.address }, 
        { -1, DMCMV_OFFICIAL_NAME, 
          BongoManageeReadString, NULL, Rules.officialName },
        { -1, RULEMV_USE_USER_RULES,
          RulesReadFlag, NULL, XPL_INT_TO_PTR (RULES_FLAG_USER_RULES) },
        { -1, RULEMV_USE_SYSTEM_RULES,
          RulesReadFlag, NULL, XPL_INT_TO_PTR (RULES_FLAG_SYSTEM_RULES) },
        { -1, DMCMV_CLIENT_POOL_STATISTICS,
          BongoManageeReadPoolStats, NULL, Rules.nmap.pool },
        { -1, DMCMV_MEM_STATISTICS,
          BongoManageeReadMemoryStats, NULL, NULL },
        { -1, NULL },
    };
        
    BongoManagee *managee;
    BongoJsonRpcServer *server;
    
    managee = BongoManageeNew (PRODUCT_NAME, 
                              MSGSRV_AGENT_RULESRV, 
                              PRODUCT_VERSION);
    
    BongoManageeAddVariables (managee, vars);
    
    BongoManageeAddMethod(managee,
                         DMCMC_SHUTDOWN,
                         BongoAgentShutdownFunc, 
                         NULL);
	
    /* Kick off a management thread */
    server = BongoManageeCreateServer(managee);
    if (!server) {
        printf ("bongorules: Couldn't start management server.\n");
        return;
    }
    
    BongoJsonRpcServerRun(server, TRUE);
}
