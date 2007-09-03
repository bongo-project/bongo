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

#include "queue.h"

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>

#include "conf.h"
#include "domain.h"
#include "messages.h"

#define STACKSPACE_Q (1024*80)
#define STACKSPACE_S (1024*80)

#define DEFAULT_MAX_LINGER (4 * 24 * 60 * 60)

#define QLIMIT_CONCURRENT 250
#define QLIMIT_SEQUENTIAL 500

int aliasCmpFunc (const void *lft, const void *rgt);

QueueConfiguration Conf = { 0, {0, }, };

long
CalculateCheckQueueLimit(unsigned long concurrent, unsigned long sequential)
{   
    if (concurrent < sequential) {
        return(sequential - ((sequential - concurrent) >> 1));
    }
    
    return(sequential);    
}

static BongoConfigItem trustedHostsConfig[] = {
    { BONGO_JSON_STRING, NULL, &Conf.trustedHosts},
    { BONGO_JSON_NULL, NULL, NULL }
};

static BongoConfigItem hostedDomainsConfig[] = {
    { BONGO_JSON_STRING, NULL, &Conf.hostedDomains},
    { BONGO_JSON_NULL, NULL, NULL }
};

static BongoConfigItem QueueConfig[] = {
    { BONGO_JSON_ARRAY, "o:hosteddomains/a", &hostedDomainsConfig },
    { BONGO_JSON_STRING, "o:postmaster/s", &Conf.postMaster },
    { BONGO_JSON_STRING, "o:officialname/s", &Conf.officialName },
    { BONGO_JSON_BOOL, "o:limitremoteprocessing/b", &Conf.deferEnabled },
    { BONGO_JSON_INT, "o:limitremotebegweekday/i", &Conf.i_deferStartWD },
    { BONGO_JSON_INT, "o:limitremotebegweekend/i", &Conf.i_deferStartWE },
    { BONGO_JSON_INT, "o:limitremoteendweekday/i", &Conf.i_deferEndWD },
    { BONGO_JSON_INT, "o:limitremoteendweekend/i", &Conf.i_deferEndWE },
    { BONGO_JSON_INT, "o:queuetuning_concurrent/i", &Conf.maxConcurrentWorkers },
    { BONGO_JSON_INT, "o:queuetuning_sequential/i", &Conf.maxSequentialWorkers },
    { BONGO_JSON_INT, "o:queuetuning_load_high/i", &Conf.loadMonitorHigh },
    { BONGO_JSON_INT, "o:queuetuning_load_low/i", &Conf.loadMonitorLow },
    { BONGO_JSON_INT, "o:queuetuning_trigger/i", &Conf.limitTrigger },
    { BONGO_JSON_BOOL, "o:queuetuning_debug/b", &Conf.debug },
    { BONGO_JSON_INT, "o:queuetimeout/i", &Conf.maxLinger },
    { BONGO_JSON_STRING, "o:quotamessage/s", &Conf.quotaMessage },
    { BONGO_JSON_INT, "o:port/i", &Agent.agent.port },
    { BONGO_JSON_BOOL, "o:forwardundeliverable_enabled/b", &Conf.forwardUndeliverableEnabled },
    { BONGO_JSON_STRING, "o:forwardundeliverable_to/s", &Conf.forwardUndeliverableAddress },
    { BONGO_JSON_INT, "o:queueinterval/i", &Conf.queueInterval },
    { BONGO_JSON_INT, "o:minimumfreespace/i", &Conf.minimumFree },
    { BONGO_JSON_ARRAY, "o:trustedhosts/a", &trustedHostsConfig },
    { BONGO_JSON_BOOL, "o:rtsantispamconfig_enabled/b", &Conf.bounceBlockSpam },
    { BONGO_JSON_INT, "o:rtsantispamconfig_delay/i", &Conf.bounceInterval },
    { BONGO_JSON_INT, "o:rtsantispamconfig_threshold/i", &Conf.bounceMax },
    { BONGO_JSON_BOOL, "o:bouncereturn/b", &Conf.b_bounceReturn },
    { BONGO_JSON_BOOL, "o:bounceccpostmaster/b", &Conf.b_bounceCCPostmaster },
    { BONGO_JSON_NULL, NULL, NULL }
};

int aliasCmpFunc (const void *lft, const void *rgt){
	struct _AliasStruct *l = (struct _AliasStruct *)(lft);
	struct _AliasStruct *r = (struct _AliasStruct *)(rgt);;
	return strcmp(l->original, r->original);
}

BOOL
ReadConfiguration (BOOL *recover)
{
    if (!MsgGetServerCredential((char *)&Conf.serverHash)) {
        return FALSE;
    }
    
    if (!ReadBongoConfiguration(QueueConfig, "queue")) {
        return FALSE;
    }
    
    *recover = MsgGetRecoveryFlag("queue");
    
    /* set some arrays up */
    if (Conf.deferEnabled) {
        Conf.deferStart[0] = Conf.i_deferStartWE;   /* sunday */
        Conf.deferStart[1] = Conf.i_deferStartWD;
        Conf.deferStart[2] = Conf.i_deferStartWD;
        Conf.deferStart[3] = Conf.i_deferStartWD;
        Conf.deferStart[4] = Conf.i_deferStartWD;
        Conf.deferStart[5] = Conf.i_deferStartWD;
        Conf.deferStart[6] = Conf.i_deferStartWE;   /* saturday */
        
        Conf.deferEnd[0] = Conf.i_deferEndWE;
        Conf.deferEnd[1] = Conf.i_deferEndWD;
        Conf.deferEnd[2] = Conf.i_deferEndWD;
        Conf.deferEnd[3] = Conf.i_deferEndWD;
        Conf.deferEnd[4] = Conf.i_deferEndWD;
        Conf.deferEnd[5] = Conf.i_deferEndWD;
        Conf.deferEnd[6] = Conf.i_deferEndWE;
    }
    
    if (Conf.debug) {
        Agent.flags |= QUEUE_AGENT_DEBUG;
    } else {
        Agent.flags &= ~QUEUE_AGENT_DEBUG;
    }
    
    Conf.queueCheck = CalculateCheckQueueLimit(Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);
    
    /* Set the globals for loadmonitor */
    Conf.defaultConcurrentWorkers = Conf.maxConcurrentWorkers;
    Conf.defaultSequentialWorkers = Conf.maxSequentialWorkers;
    
    strcpy(Conf.spoolPath, XPL_DEFAULT_SPOOL_DIR);
    MsgMakePath(Conf.spoolPath);
    
    sprintf(Conf.queueClientsPath, "%s/qclients", MsgGetDir(MSGAPI_DIR_DBF, NULL, 0));
    
    Conf.bounceMaxBodySize = 0;
    /* Some sanity checking on the QLimit stuff to prevent running out of memory */
    if (XplGetMemAvail() < (unsigned long)((STACKSPACE_Q + STACKSPACE_S) * Conf.maxSequentialWorkers)) {
        Conf.maxSequentialWorkers = (XplGetMemAvail() / (STACKSPACE_Q+STACKSPACE_S)) / 2;
        Conf.maxConcurrentWorkers = Conf.maxSequentialWorkers / 2;
        Conf.queueCheck = CalculateCheckQueueLimit(Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);

        XplBell();
        XplConsolePrintf("bongoqueue: WARNING - Tuning parameters adjusted to %ld par./%ld seq.\r\n", Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);
        XplBell();

        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_QLIMITS_ADJUSTED, LOG_WARNING, 0, NULL, NULL, Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers, NULL, 0);
        Conf.defaultConcurrentWorkers = Conf.maxConcurrentWorkers;
        Conf.defaultSequentialWorkers = Conf.maxSequentialWorkers;
    }

    BongoArraySort(Conf.trustedHosts, (ArrayCompareFunc)strcmp);
    
    if (Conf.b_bounceReturn) {
        Conf.bounceHandling |= BOUNCE_RETURN;
    } else {
        Conf.bounceHandling &= ~BOUNCE_RETURN;
    }
    
    if (Conf.b_bounceCCPostmaster) {
        Conf.bounceHandling |= BOUNCE_CC_POSTMASTER;
    } else {
        Conf.bounceHandling &= ~BOUNCE_RETURN;
    }
    
    /* now let's iterate over the hostedDomains and read in any aliasing information for those domains */
    {
    	Conf.aliasList = BongoArrayNew(sizeof(AliasStruct), Conf.hostedDomains->len);

	unsigned int x;
	for(x=0;x<Conf.hostedDomains->len;x++) {
		unsigned char *aconfig;
		unsigned char path[100];
		BongoJsonNode *node;
		struct _AliasStruct *a = MemMalloc(sizeof(AliasStruct));

		a->original = MemStrdup(BongoArrayIndex(Conf.hostedDomains, unsigned char *, x));
		a->to = NULL;
		a->aliases = NULL;

		sprintf(path, "aliases/%s", a->original);
		if(NMAPReadConfigFile(path, &aconfig)) {
			if (BongoJsonParseString(aconfig, &node) == BONGO_JSON_OK) {
				BongoJsonObject *obj;

				BongoJsonJPathGetString(node, "o:domainalias/s", (char **)&(a->to));

				/* now get any specific aliases */
				if (BongoJsonJPathGetObject(node, "o:aliases/o", &obj) == BONGO_JSON_OK) {
					BongoJsonObjectIter iter;

					a->aliases = BongoArrayNew(sizeof(AliasStruct), 1);

					BongoJsonObjectIterFirst(obj, &iter);
					while (iter.key) {
						/* TODO: do i need to parse the addresses?
						 * 	I'd need to parse original and only use the user portion
						 * 	I'd need to parse the to and append the domain if none exists
						 */
						struct _AliasStruct *b = MemMalloc(sizeof(AliasStruct));
						b->original = MemStrdup(iter.key);
						b->to = MemStrdup(BongoJsonNodeAsString(iter.value));
						b->aliases = NULL;

						BongoArrayAppendValue(a->aliases, *b);

						BongoJsonObjectIterNext(obj, &iter);
					}
					BongoArraySort(a->aliases, (ArrayCompareFunc)aliasCmpFunc);
				}
				BongoJsonNodeFree(node);
			}
		}

		BongoArrayAppendValue(Conf.aliasList, *a);
	}

	/* sort the list for speed later */
	BongoArraySort(Conf.aliasList, (ArrayCompareFunc)aliasCmpFunc);
    }
    return TRUE;
}

void
CheckConfig(BongoAgent *agent)
{
/* TODO:  Figure out how we are going to handle this type of situation */
    return;
}
