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
#include <memmgr.h>
#include <calcmd.h>

int
main(int argc, char **argv)
{
    int i;
    time_t basetime = 1126981800; /* 2005-09-17 Saturday */
    CalCmdCommand cmd;

    CalCmdSetTestMode (basetime);

    MemoryManagerOpen("parsecmd test");

    for (i = 1; i < argc; i++) {
        if (0 == strcmp ("-d", argv[i])) {
            CalCmdSetDebugMode (1);
            continue;
        }
        if (0 == strcmp ("-v", argv[i])) {
            CalCmdSetVerboseDebugMode (1);
            continue;
        }
        if (!CalCmdParseCommand (argv[i], &cmd, BongoCalTimezoneGetUtc())) {
            continue;
        }

        fprintf (stdout,

/*
                 "\n    %s"
                 "\n    %04d-%02d-%02d %02d:%02d"
                 "\n    %04d-%02d-%02d %02d:%02d\n\n",
*/
                 "%04d-%02d-%02d %02d:%02d %04d-%02d-%02d %02d:%02d %s\n",

                 cmd.begin.year, cmd.begin.month + 1, cmd.begin.day,
                 cmd.begin.hour, cmd.begin.minute,
                 cmd.end.year, cmd.end.month + 1, cmd.end.day,
                 cmd.end.hour, cmd.end.minute,
                 cmd.data);
    }

    MemoryManagerClose("parsecmd test");

    return 0;
}
