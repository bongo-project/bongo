/* This program is free software, licensed under the terms of the GNU GPL.
 * See the Bongo COPYING file for full details
 * Copyright (c) 2007 Alex Hudson
 */

#include <xpl.h>
#include <xpldns.h>
#include <msgapi.h>
#include <arpa/inet.h>
#include "config.h"

#include <libintl.h>
#define _(x) gettext(x)

void
usage(void) {
        char *text = 
                "bongo-testtool - Test parts of the Bongo system.\n\n"
                "Usage: bongo-testtool [command]\n\n"
                "Commands:\n"
		" checkmx <domain>	Search for mail exchangers\n"
                "";

        XplConsolePrintf("%s", text);
}

void
LookupMxRecords(const char *domain)
{
	XplDns_MxLookup *mx;
	XplDns_IpList *list;

	mx = XplDnsNewMxLookup(domain);
	if (!mx || mx->status != XPLDNS_SUCCESS) {
		XplConsolePrintf(_("ERROR: Unable to find mail exchangers for %s\n"), domain);
		return;
	}
	
	// FIXME. We shouldn't try to relay to MXes who are lower preference
	// than we are, if we're in the list (to prevent mail loops).
	// In practice, this means stopping as soon as come to an IP we
	// recognise as being ours.
	while((list = XplDnsNextMxLookupIpList(mx)) != NULL) {
		int x;

		XplConsolePrintf(_("%s (preference: %d):\n"), 
			mx->current_mx,	mx->preference);
		for (x = 0; x < list->number; x++) {
			struct sockaddr_in addr;
			addr.sin_addr.s_addr = list->ip_list[x];
			XplConsolePrintf(" - %s\n", inet_ntoa(addr.sin_addr));
		}
		MemFree(list);
	}

	XplDnsFreeMxLookup(mx);
}

int 
main(int argc, char *argv[]) {
	int next_arg = 0;
	int command = 0;

	if (!MemoryManagerOpen("bongo-testtool")) {
		XplConsolePrintf(_("ERROR: Failed to initialize memory manager\n"));
		return 1;
	}
	
	// parse options
	while (++next_arg < argc && argv[next_arg][0] == '-') {
		printf(_("Unrecognized option: %s\n"), argv[next_arg]);
	}

	// parse command 
	if (next_arg < argc) {
		if (!strcmp(argv[next_arg], "checkmx")) { 
			command = 1;
		} else {
			printf(_("Unrecognized command: %s\n"), argv[next_arg]);
		}
	} else {
		printf(_("ERROR: No command specified\n"));
		usage();
		return 1;
	}

	XplInit();

	if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {    
		printf(_("bongo-testtool: Could not drop to unprivileged user '%s'\n"), MsgGetUnprivilegedUser());
		exit(-1);
	}

	switch(command) {
		case 1:
			next_arg++;
			if (next_arg >= argc) {
				printf(_("Usage: checkmx <domain>\n"));
			} else {
				LookupMxRecords(argv[next_arg]);
			}
			break;
		default:
			break;
	}

	MemoryManagerClose("bongo-testtool");
	exit(0);
}
