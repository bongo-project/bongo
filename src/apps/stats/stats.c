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

HSGlobals BongoStats;

void 
StatPrintNumber(unsigned long number, unsigned char *buffer)
{
    register long x;
    register long len;

    if (number < 1000) {
        len = sprintf(buffer, "        %3lu", number);
    } else if (number < 1000000) {
        x = number % 1000;
        len = sprintf(buffer, "    %3lu,%03lu", (number - x) / 1000, x);
    } else {
        x = number / 1000000;
        len = sprintf(buffer, "%3lu,%03lu,%03lu",  number / 1000000, (number - x * 1000000) / 1000, number % 1000);
    }

    return;
}

void 
StatPrintFloat(unsigned long number, unsigned long divideBy, unsigned char *buffer)
{
    register long x;
    register long y;

    if (divideBy == 0) {
        x = 0;
        y = 0;
    } else {
        x = (number * 100) / divideBy;
        y = x % 100;
    }

    sprintf(buffer, "%3lu.%02lu", x / 100, y);

    return;
}


void 
StatPrintTime(unsigned long seconds, unsigned char *buffer)
{
    long d;
    long h;
    long m;
    long s;
    long a;
    long b;

    d = seconds / 86400;
    a = d * 86400;
    h = (seconds - a) / 3600;
    b = h * 3600;
    m = (seconds - (a + b)) / 60;
    s = seconds - (a + b + m * 60);

    sprintf(buffer, "%3lud %02luh %02lum %02lus", d, h, m, s);

    return;
}

int 
LoadStatistics(StatisticsStruct *stats)
{
    int ccode;
    unsigned long i;
    unsigned long *statistic;

    if (BongoStats.selected.conn) {
        if (((ccode = ConnWrite(BongoStats.selected.conn, "STAT0\r\n", 7)) != -1) 
                && ((ccode = ConnFlush(BongoStats.selected.conn)) != -1) 
                && ((ccode = ConnReadLine(BongoStats.selected.conn, BongoStats.selected.line, CONN_BUFSIZE)) != -1) 
                && (XplStrNCaseCmp(BongoStats.selected.line, DMCMSG1001RESPONSES_COMMING, 5) == 0) 
                && (atol(BongoStats.selected.line + 5) == (sizeof(StatisticsStruct) / sizeof(unsigned long)))) {
            statistic = &(stats->ModulesLoaded);

            for (i = 0; (ccode != -1) && (i < sizeof(StatisticsStruct) / sizeof(unsigned long)); i++, statistic++) {
                if ((ccode = ConnReadLine(BongoStats.selected.conn, BongoStats.selected.line, CONN_BUFSIZE)) != -1) {
                    *statistic = atol(BongoStats.selected.line);
                }
            }

            if (ccode != -1) {
                if (i == sizeof(StatisticsStruct) / sizeof(unsigned long)) {
                    ccode = ConnRead(BongoStats.selected.conn, BongoStats.selected.line, CONN_BUFSIZE);
                } else {
                    ccode = -1;
                }
            }
        } else if (ccode != -1) {
            ccode = -1;
        }
    } else {
        ccode = -1;
    }

    return(ccode);
}

int 
LoadSpamStatistics(AntispamStatisticsStruct *spamStats)
{
    int ccode;
    unsigned long i;
    unsigned long *statistic;

    if (BongoStats.selected.conn) {
        if (((ccode = ConnWrite(BongoStats.selected.conn, "STAT1\r\n", 7)) != -1) 
                && ((ccode = ConnFlush(BongoStats.selected.conn)) != -1) 
                && ((ccode = ConnReadLine(BongoStats.selected.conn, BongoStats.selected.line, CONN_BUFSIZE)) != -1) 
                && (XplStrNCaseCmp(BongoStats.selected.line, DMCMSG1001RESPONSES_COMMING, 5) == 0) 
                && (atol(BongoStats.selected.line + 5) == (sizeof(AntispamStatisticsStruct) / sizeof(unsigned long)))) {
            statistic = &(spamStats->QueueSpamBlocked);

            for (i = 0; (ccode != -1) && (i < sizeof(AntispamStatisticsStruct) / sizeof(unsigned long)); i++, statistic++) {
                if ((ccode = ConnReadLine(BongoStats.selected.conn, BongoStats.selected.line, CONN_BUFSIZE)) != -1) {
                    *statistic = atol(BongoStats.selected.line);
                }
            }

            if (ccode != -1) {
                if (i == sizeof(AntispamStatisticsStruct) / sizeof(unsigned long)) {
                    ccode = ConnRead(BongoStats.selected.conn, BongoStats.selected.line, CONN_BUFSIZE);
                } else {
                    ccode = -1;
                }
            }
        } else if (ccode != -1) {
            ccode = -1;
        }
    } else {
        ccode = -1;
    }

    return(ccode);
}

int 
ReleaseMessagingServer(void)
{
    int ccode = 0;

    if (BongoStats.selected.conn) {
        if ((ccode = ConnWrite(BongoStats.selected.conn, "QUIT\r\n", 6)) != -1) {
            ccode = ConnFlush(BongoStats.selected.conn);
        }

        ConnClose(BongoStats.selected.conn, 0);
        ConnFree(BongoStats.selected.conn);

        BongoStats.selected.conn = NULL;
    }

    return(ccode);
}

int 
ConnectToMessagingServer(unsigned char *dn)
{
    int ccode;
    unsigned long used;
    unsigned long port[2];
    MDBValueStruct *config;

    if (BongoStats.selected.conn) {
        ReleaseMessagingServer();
    }

    port[0] = DMC_MANAGER_SSL_PORT;
    port[1] = DMC_MANAGER_PORT;

    BongoStats.selected.conn = ConnAlloc(TRUE);

    config = MDBCreateValueStruct(BongoStats.directory, NULL);
    if (config) {
        MDBRead(dn, MSGSRV_A_CONFIGURATION, config);
    } else {
        return(FALSE);
    }

    for (used = 0; used < config->Used; used++) {
        if (XplStrNCaseCmp(config->Value[used], "DMC: ManagerPort=", 17) == 0) {
            if (isdigit(config->Value[used][17])) {
                port[1] = atol(config->Value[used] + 17);
            } else {
                port[1] = -1;
            }
        } else if (XplStrNCaseCmp(config->Value[used], "DMC: ManagerSSLPort=", 20) == 0) {
            if (isdigit(config->Value[used][20])) {
                port[0] = atol(config->Value[used] + 20);
            } else {
                port[0] = -1;
            }
        }
    }

    MDBFreeValues(config);

    if (((port[0] != (unsigned long)-1) || (port[1] != (unsigned long)-1)) && (MDBRead(dn, MSGSRV_A_IP_ADDRESS, config))) {
        ccode = 0;

        BongoStats.selected.conn->socketAddress.sin_family = AF_INET;
        BongoStats.selected.conn->socketAddress.sin_addr.s_addr = inet_addr(config->Value[0]);
    } else {
        ccode = -1;
    }

    MDBDestroyValueStruct(config);
    config = NULL;

    if (!ccode) {
        if (BongoStats.ssl.enable && ((BongoStats.selected.conn->socketAddress.sin_port = htons((unsigned short)port[0])) != (unsigned short)-1)) {
            ccode = ConnConnect(BongoStats.selected.conn, NULL, 0, BongoStats.ssl.context);
        } else if ((BongoStats.selected.conn->socketAddress.sin_port = htons((unsigned short)port[1])) != (unsigned short)-1) {
            ccode = ConnConnect(BongoStats.selected.conn, NULL, 0, NULL);
        } else {
            ccode = -1;
        }

        if (ccode != -1) {
            ccode = ConnRead(BongoStats.selected.conn, BongoStats.selected.line, CONN_BUFSIZE);
        }
    }

    return(ccode);
}

static BOOL 
ReadConfiguration(int argc, char *argv[])
{
    BOOL result = FALSE;
    MDBValueStruct *config;

    result = MsgGetConfigProperty(BongoStats.serverDN, 
				  MSGSRV_CONFIG_PROP_MESSAGING_SERVER);
    return(result);
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    static BOOL checked = FALSE;

    if (!checked) {
        checked = TRUE;
        BongoStats.state = BONGOSTATS_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(BongoStats.sem.shutdown);

        XplWaitOnLocalSemaphore(BongoStats.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (BongoStats.state < BONGOSTATS_STATE_UNLOADING) {
                BongoStats.state = BONGOSTATS_STATE_UNLOADING;
            }

            break;
        }

        case SIGINT:
        case SIGTERM: {
            if (BongoStats.state == BONGOSTATS_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (BongoStats.state < BONGOSTATS_STATE_STOPPING) {
                BongoStats.state = BONGOSTATS_STATE_STOPPING;
            }

            break;
        }

        default: {
            break;
        }
    }

    return;
}

int 
BongoStatsInitialize(int argc, char *argv[])
{
    XplSignalHandler(SignalHandler);
	XplRenameThread(GetThreadID(), "Bongo Statistics Utility");

    BongoStats.id.main = XplGetThreadID();
    BongoStats.id.group = XplGetThreadGroupID();

    BongoStats.state = BONGOSTATS_STATE_INITIALIZING;

    BongoStats.selected.conn = NULL;
    BongoStats.selected.dn[0] = '\0';
    BongoStats.selected.rdn[0] = '\0';

    BongoStats.ssl.conn = NULL;
    BongoStats.ssl.enable = FALSE;
    BongoStats.ssl.context = NULL;
    BongoStats.ssl.config.options = 0;

    BongoStats.directory = NULL;

    BongoStats.start = time(NULL);

    if (MemoryManagerOpen("BongoStats") == TRUE) {
        XplOpenLocalSemaphore(BongoStats.sem.main, 0);
        XplOpenLocalSemaphore(BongoStats.sem.shutdown, 1);
    } else {
        XplConsolePrintf("bongostats: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    ConnStartup(3 * 60, TRUE);

    MDBInit();
    BongoStats.directory = (MDBHandle)MsgInit();
    if (BongoStats.directory == NULL) {
        XplBell();
        XplConsolePrintf("bongostats: Invalid directory credentials; exiting!\r\n");
        XplBell();

        MemoryManagerClose("BongoStats");

        return(-1);
    }

    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "stats");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    SetCurrentNameSpace(NWOS2_NAME_SPACE);
    SetTargetNameSpace(NWOS2_NAME_SPACE);

    if (ReadConfiguration(argc, argv)) {
        BongoStats.ssl.enable = FALSE;
        BongoStats.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
        BongoStats.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
        BongoStats.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

        BongoStats.ssl.context = ConnSSLContextAlloc(&(BongoStats.ssl.config));
        if (BongoStats.ssl.context) {
            BongoStats.ssl.enable = TRUE;
        }

        BongoStats.state = BONGOSTATS_STATE_RUNNING;

        if ((BongoStatsUIStart() == 0) && (ConnectToMessagingServer(BongoStats.serverDN) != -1)) {
            BongoStatsUIRun();
            BongoStatsUIStop();
        }

        ReleaseMessagingServer();
    } else {
        BongoStats.state = BONGOSTATS_STATE_STOPPING;
    }

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();
    return(0);
}
