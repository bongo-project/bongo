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

#include <errno.h>
#include <bongoutil.h>
#include <connio.h>

#include <mdb.h>
#include <msgapi.h>
#include <management.h>

#include "stats.h"

static void 
StatCommand(void)
{
    long seconds;
    unsigned char buffer1[20];
    unsigned char buffer2[20];
    unsigned char buffer3[20];

    seconds = BongoStats.server.current.Uptime;

    XplConsolePrintf("\r\nBongo Statistics:\r\n\r\n");
    XplConsolePrintf("                         local       remote      per/s\r\n");

    StatPrintNumber(BongoStats.server.current.MsgQueuedLocal, buffer1);
    StatPrintNumber(BongoStats.server.current.MsgQueuedRemote, buffer2);
    XplConsolePrintf("Messages queued:   %s  %s\r\n", buffer1, buffer2);

    StatPrintNumber(BongoStats.server.current.MsgReceivedLocal, buffer1);
    StatPrintNumber(BongoStats.server.current.MsgReceivedRemote, buffer2);
    StatPrintFloat((BongoStats.server.current.MsgReceivedLocal+BongoStats.server.current.MsgReceivedRemote), seconds, buffer3);
    XplConsolePrintf("Messages received: %s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.MsgStoredLocal, buffer1);
    StatPrintNumber(BongoStats.server.current.MsgStoredRemote, buffer2);
    StatPrintFloat((BongoStats.server.current.MsgStoredLocal+BongoStats.server.current.MsgStoredRemote), seconds, buffer3);
    XplConsolePrintf("Messages delivered:%s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.DeliveryFailedLocal, buffer1);
    StatPrintNumber(BongoStats.server.current.DeliveryFailedRemote, buffer2);
    StatPrintFloat((BongoStats.server.current.DeliveryFailedLocal+BongoStats.server.current.DeliveryFailedRemote), seconds, buffer3);
    XplConsolePrintf("Messages failed:   %s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.RcptStoredLocal, buffer1);
    StatPrintNumber(BongoStats.server.current.RcptStoredRemote, buffer2);
    StatPrintFloat((BongoStats.server.current.RcptStoredLocal+BongoStats.server.current.RcptStoredRemote), seconds, buffer3);
    XplConsolePrintf("Recipients:        %s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.ByteReceivedLocal, buffer1);
    StatPrintNumber(BongoStats.server.current.ByteReceivedRemote, buffer2);
    StatPrintFloat((BongoStats.server.current.ByteReceivedLocal+BongoStats.server.current.ByteReceivedRemote), seconds, buffer3);
    XplConsolePrintf("KBytes received:   %s  %s     %s\r\n", buffer1, buffer2, buffer3);

    if (BongoStats.server.current.ByteReceivedCount>0) {
        StatPrintNumber((BongoStats.server.current.ByteReceivedLocal/BongoStats.server.current.ByteReceivedCount), buffer1);
        StatPrintNumber((BongoStats.server.current.ByteReceivedRemote/BongoStats.server.current.ByteReceivedCount), buffer2);
        StatPrintFloat((BongoStats.server.current.ByteReceivedLocal+BongoStats.server.current.ByteReceivedRemote)/BongoStats.server.current.ByteReceivedCount, seconds, buffer3);
        XplConsolePrintf("Average received:  %s  %s     %s\r\n", buffer1, buffer2, buffer3);
    }

    StatPrintNumber(BongoStats.server.current.ByteStoredLocal, buffer1);
    StatPrintNumber(BongoStats.server.current.ByteStoredRemote, buffer2);
    StatPrintFloat((BongoStats.server.current.ByteStoredLocal+BongoStats.server.current.ByteStoredRemote), seconds, buffer3);
    XplConsolePrintf("KBytes delivered:  %s  %s     %s\r\n", buffer1, buffer2, buffer3);

    if (BongoStats.server.current.ByteStoredCount>0) {
        StatPrintNumber((BongoStats.server.current.ByteStoredLocal/BongoStats.server.current.ByteStoredCount), buffer1);
        StatPrintNumber((BongoStats.server.current.ByteStoredRemote/BongoStats.server.current.ByteStoredCount), buffer2);
        StatPrintFloat((BongoStats.server.current.ByteStoredLocal+BongoStats.server.current.ByteStoredRemote)/BongoStats.server.current.ByteStoredCount, seconds, buffer3);
        XplConsolePrintf("Average delivered: %s  %s     %s\r\n", buffer1, buffer2, buffer3);
    }

    XplConsolePrintf("\n                         curr.        total\r\n");

    StatPrintNumber(BongoStats.server.current.IncomingClients, buffer1);
    StatPrintNumber(BongoStats.server.current.ServicedClients, buffer2);
    StatPrintFloat((BongoStats.server.current.IncomingClients+BongoStats.server.current.ServicedClients), seconds, buffer3);
    XplConsolePrintf("Client connections:%s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.IncomingServers, buffer1);
    StatPrintNumber(BongoStats.server.current.ServicedServers, buffer2);
    StatPrintFloat((BongoStats.server.current.IncomingServers+BongoStats.server.current.ServicedServers), seconds, buffer3);
    XplConsolePrintf("Server connections:%s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.OutgoingServers, buffer1);
    StatPrintNumber(BongoStats.server.current.ClosedOutServers, buffer2);
    StatPrintFloat((BongoStats.server.current.OutgoingServers+BongoStats.server.current.ClosedOutServers), seconds, buffer3);
    XplConsolePrintf("Outg. connections: %s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.CurrentStoreAgents, buffer1);
    StatPrintNumber(BongoStats.server.current.ServicedStoreAgents, buffer2);
    StatPrintFloat(BongoStats.server.current.ServicedStoreAgents, seconds, buffer3);
    XplConsolePrintf("NMAP connections:  %s  %s     %s\r\n", buffer1, buffer2, buffer3);

    StatPrintNumber(BongoStats.server.current.CurrentNMAPtoNMAPAgents, buffer1);
    StatPrintNumber(BongoStats.server.current.ServicedNMAPtoNMAPAgents, buffer2);
    StatPrintFloat(BongoStats.server.current.ServicedNMAPtoNMAPAgents, seconds, buffer3);
    XplConsolePrintf("NMAP to NMAP conns:%s  %s     %s\r\n", buffer1, buffer2, buffer3);

    XplConsolePrintf("\nWrong passwords:       %7lu         Uptime\r\n", BongoStats.server.current.WrongPassword);

    StatPrintTime(seconds, buffer2);
    XplConsolePrintf("Unauthorized attempts: %7lu   %s\r\n", BongoStats.server.current.UnauthorizedAccess, buffer2);

    return;
}

static void 
SpamCommand(void)
{
    unsigned char buffer[20];

    XplConsolePrintf("\r\nBongo Antispam Statistics:\r\n\r\n");

    StatPrintNumber(BongoStats.spam.current.QueueSpamBlocked, buffer);
    XplConsolePrintf("Bounces refused:                        %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.AddressBlocked, buffer);
    XplConsolePrintf("Access from blocked addresses:          %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.MAPSBlocked, buffer);
    XplConsolePrintf("Access blocked due to RBL list:         %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.DeniedRouting, buffer);
    XplConsolePrintf("Remote routing attempts denied:         %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.NoDNSEntry, buffer);
    XplConsolePrintf("Access blocked due to missing DNS entry:%s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.MessagesScanned, buffer);
    XplConsolePrintf("Messages scanned:                       %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.AttachmentsScanned, buffer);
    XplConsolePrintf("Message-attachments scanned:            %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.AttachmentsBlocked, buffer);
    XplConsolePrintf("Messages with attachments blocked:      %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.VirusesFound, buffer);
    XplConsolePrintf("Messages with viruses found:            %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.VirusesBlocked, buffer);
    XplConsolePrintf("Messages with viruses blocked:          %s\r\n", buffer);

    StatPrintNumber(BongoStats.spam.current.VirusesCured, buffer);
    XplConsolePrintf("Messages with viruses cured:            %s\r\n", buffer);

    return;
}

int 
BongoStatsUIStart(void)
{
    return(0);
}

int 
BongoStatsUIRun(void)
{
    LoadStatistics(&BongoStats.server.current);
    StatCommand();

    LoadSpamStatistics(&BongoStats.spam.current);
    SpamCommand();

    return(0);
}

void 
BongoStatsUIStop(void)
{
    return;
}

int 
main(int argc, char *argv[])
{
    BongoStatsInitialize(argc, argv);

    return(0);
}
