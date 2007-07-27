/* This program is free software, licensed under the terms of the GNU GPL.
 * See the Bongo COPYING file for full details
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * Copyright (c) 2007 Alex Hudson
 */

#include <config.h>
#include <xpl.h>
#include <nmap.h>
#include <bongoutil.h>
#include <msgapi.h>
#include <nmlib.h>
#include <sys/wait.h>
#include <sys/resource.h>
#include <time.h>
#include <unistd.h>
#include <stdio.h>
#include <getopt.h>

#include <libintl.h>
#define _(x) gettext(x)

#define LOCKFILE            XPL_DEFAULT_WORK_DIR "/bongomanager.pid"
#define SLAPD_LOCKFILE      XPL_DEFAULT_WORK_DIR "/bongo-slapd.pid"

/* Don't restart agents if they crash n times within m seconds of each other */
#define MAX_CRASHES 3
#define CRASH_WINDOW 10

typedef struct {
    const char *program;
    const char *dn;
    int priority;
    BOOL willCallback;
} BongoAgentSpec;

typedef struct {
    BongoAgentSpec spec;

    /* Agent configuration */
    int enabled;

    /* Execution state */
    pid_t pid;
    int exitStatus;
    BOOL waitingForCallback;

    /* Crashy agent detection */
    BOOL crashed;
    time_t lastCrash;
    int numCrashes;
    BOOL crashy;
} BongoAgent;

static BongoArray *AllAgents;
static pid_t LeaderPID = 0;
static pid_t SlapdPID = 0;
static BOOL Exiting = FALSE;
static BOOL ChildError = FALSE;
static BOOL ReloadNow = FALSE;
static int HighestPriority = 0;

MDBHandle DirectoryHandle = NULL;
char ServerDN[MDB_MAX_OBJECT_CHARS + 1];

static BongoAgent *
FindAgentByPid(pid_t pid)
{
    unsigned int i;
    
    for (i = 0; i < AllAgents->len; i++) {
        BongoAgent *agent = &BongoArrayIndex(AllAgents, BongoAgent, i);
        if (agent->pid == pid) {
            return agent;
        }
    }

    return NULL;
}

static BOOL
SetupAgentList(void)
{
    AllAgents = BongoArrayNew(sizeof(BongoAgent), 1);
    
    BongoAgent store = {{0,} };
    store.spec.program = "bongostore";
    store.spec.priority = 0;
    store.enabled = TRUE;
    BongoArrayAppendValue(AllAgents, store);

    return TRUE;
}

static int
AgentsStillRunning(void)
{
    unsigned int i;
    int ret = 0;
    
    for (i = 0; i < AllAgents->len; i++) {
        BongoAgent *agent = &BongoArrayIndex(AllAgents, BongoAgent, i);
        if (agent->pid != 0) {
            ret++;
        }
    }    

    return ret;
}

static void 
BlameAgents(void)
{
    unsigned int i;
    for (i = 0; i < AllAgents->len; i++) {
        BongoAgent *agent = &BongoArrayIndex(AllAgents, BongoAgent, i);
        if (agent->pid != 0) {
            fprintf(stderr, "%s ", agent->spec.program);
        }
    }
}

static void
Reap(void)
{
    BongoAgent *agent;
    int status;
    
    pid_t pid = waitpid(-1, &status, WNOHANG);
    while (pid > 0) {
        agent = FindAgentByPid(pid);
        if (agent) {
            agent->pid = 0;
            agent->exitStatus = status;
            agent->waitingForCallback = FALSE;
            if (!Exiting && WIFSIGNALED(status)) {
                fprintf(stderr, _("bongomanager: %s terminated with signal %d.\n"), 
                       agent->spec.program, WTERMSIG(status));
                if (WTERMSIG(status) != SIGTERM) {
                    agent->crashed = TRUE;
                }
            } else if (!Exiting) {
                fprintf(stderr, _("bongo-manager: %s exited\n"), agent->spec.program);
            }
        }

        pid = waitpid(-1, &status, WNOHANG);
    }
}

static BOOL
WaitingForCallbacks(void)
{
    unsigned int i;

    for (i = 0; i < AllAgents->len; i++) {
        BongoAgent *agent = &BongoArrayIndex(AllAgents, BongoAgent, i);
        if (agent->waitingForCallback) {
            return TRUE;
        }
    }

    return FALSE;
}


static BOOL
WaitForCallbacks(void)
{
    /* FIXME: this isn't implemented yet */
    int i;
    for (i = 0; i < 10; i++) {
        if (!WaitingForCallbacks()) {
            return TRUE;
        }
        XplDelay(500);
    }

    return FALSE;
}

static void
StartAgent(BongoAgent *agent)
{
    agent->waitingForCallback = agent->spec.willCallback;

    fflush(stdout);

    /* Give the agent root so it can bind to ports */
    XplSetEffectiveUserId(0);

    agent->pid = fork();
    if (agent->pid == 0) { /* child */
        char *args[2];
        char path[XPL_MAX_PATH];
        int err;
        struct rlimit rlim;
        
        rlim.rlim_max = 20480;
        rlim.rlim_cur = 20480;
        err = setrlimit(RLIMIT_NOFILE, &rlim);
        
        if (err != 0) {
            fprintf(stderr, _("couldn't set rlimit: %s\n"), strerror(errno));
        }

        if (LeaderPID == 0) {
            setpgid(0, 0);
        } else {
            setpgid(0, LeaderPID);
        }

        if (agent->spec.program[0] == '/') {
            BongoStrNCpy(path, agent->spec.program, XPL_MAX_PATH);
        } else {
            snprintf(path, XPL_MAX_PATH, "%s/%s", XPL_DEFAULT_BIN_DIR, agent->spec.program);
        }

        args[0] = path;
        args[1] = NULL;
        
        execv(path, args);
        fprintf(stderr, _("failed to exec %s (%s)\n"), args[0], strerror(errno));
        exit(1);
    } else { /* parent */
        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            fprintf(stderr, _("bongo-manager: Could not drop to unprivileged user '%s', Exiting.\n"),
                   MsgGetUnprivilegedUser());
            exit(1);
        }
        
        if (LeaderPID == 0) {
            LeaderPID = agent->pid;
        }
    }
}

static void
ResetCrashiness(void) 
{
    unsigned int i;

    for (i = 0; i < AllAgents->len; i++) {
        BongoAgent *agent = &BongoArrayIndex(AllAgents, BongoAgent, i);
        agent->crashy = FALSE;
        agent->lastCrash = 0;
        agent->numCrashes = 0;
    }
}

static BOOL
CheckCrashyAgent(BongoAgent *agent)
{
    if (agent->crashy) {
        return TRUE;
    }

    if (!agent->crashed) {
        return FALSE;
    }
    
    agent->crashed = FALSE;
    
    time_t crashTime = time(NULL);
    if (crashTime - agent->lastCrash > CRASH_WINDOW) {
        /* Outside the window, reset the crash timer */
        agent->numCrashes = 0;
    }

    agent->numCrashes++;
    
    agent->lastCrash = crashTime;

    if (agent->numCrashes == MAX_CRASHES) {
        fprintf(stderr, _("bongo-manager: %s has crashed %d times within %d seconds of each other, not restarting.\n"), 
               agent->spec.program, MAX_CRASHES, CRASH_WINDOW);
        agent->crashy = TRUE;
        return TRUE;
    }

    return FALSE;
}

static BOOL
StartAgentsWithPriority(int priority, BOOL onlyCrashed, BOOL printMessage)
{
    unsigned int i;
    
    for (i = 0; i < AllAgents->len; i++) {
        BongoAgent *agent = &BongoArrayIndex(AllAgents, BongoAgent, i);
        if (agent->enabled 
            && agent->spec.priority == priority
            && agent->pid == 0
            && (!onlyCrashed || agent->crashed)
            && !CheckCrashyAgent(agent)) {
            if (printMessage) {
                printf(_("bongo-manager: starting %s\n"), agent->spec.program);
            }
            StartAgent(agent);
        }
    }

    return WaitForCallbacks();
}

static BOOL
LoadAgentConfiguration()
{
	unsigned char *config;
	BOOL retcode = FALSE;
	BongoJsonNode *node;
	BongoJsonResult res;
	BongoArray *agentlist;
	unsigned int i;

	if (! NMAPReadConfigFile("manager", &config)) {
		printf(_("bongo-manager: couldn't read config from store\n"));
		return FALSE;
	}
	
	if (BongoJsonParseString(config, &node) != BONGO_JSON_OK) {
		printf(_("bongo-manager: couldn't parse JSON config\n"));
		goto finish;
	}
	res = BongoJsonJPathGetArray(node, "o:agents/a", &agentlist);
	if (res != BONGO_JSON_OK) {
		printf(_("manager: couldn't find agent list\n"));
		goto finish;
	}

	for (i = 0; i < agentlist->len; i++) {
	        BongoJsonNode *anode = BongoJsonArrayGet(agentlist, i);
		BongoAgent agent = {{0,}};

		BongoJsonJPathGetBool(anode, "o:enabled/b", &agent.enabled);
		BongoJsonJPathGetInt(anode, "o:pri/i", &agent.spec.priority);
		BongoJsonJPathGetString(anode, "o:name/s", &agent.spec.program);

		BongoArrayAppendValue(AllAgents, agent);
		HighestPriority = max(HighestPriority, agent.spec.priority); 
	}
	retcode = TRUE;

finish:
	BongoJsonNodeFree(node);

	return retcode;
}

static void
StartStore()
{
	StartAgentsWithPriority(0, FALSE, TRUE);
}

static void
StartAgents(BOOL onlyCrashed, BOOL printMessage)
{
    int i;
    
    for (i = 1; i <= HighestPriority; i++) {
        StartAgentsWithPriority(i, onlyCrashed, printMessage);
    }
}

static void
SignalHandler(int signo)
{
    switch(signo) {
    case SIGCHLD :
        return;
    case SIGINT:
    case SIGTERM :
        if (!Exiting) {
            /* Politely request that the children exit */
            if (LeaderPID != 0) {
                killpg(LeaderPID, signo);
            }
            
            Exiting = TRUE;
        } else {
            /* Second time through, kill the children with prejudice */
            if (LeaderPID != 0) {
                killpg(LeaderPID, SIGKILL);
            }
            if (SlapdPID != 0) {
                kill(SlapdPID, SIGKILL);
            }
            XplExit(0);
        }
        break;
    case SIGUSR1 :
        ReloadNow = TRUE;
        break;
    }    
}

static int
Lock(BOOL force)
{
    int fd;
    int remaining;
    int written;
    int ret;
    int mode;
    pid_t pid;
    char pidstr[20];
    
    mode = O_CREAT | O_WRONLY;
    if (!force) {
        mode |= O_EXCL;
    }

    fd = open(LOCKFILE, mode, S_IREAD | S_IWRITE);
    if (fd == -1) {
        return -1;
    }

    pid = getpid();
    if ((unsigned long)pid > 999999) {
	/* pid < 0 || pid > pidmax ceiling on Solaris */
        fprintf(stderr, _("bongo-manager: unlikely pid, Exiting\n"));
        exit(1);
    }

    remaining = snprintf(pidstr, sizeof(pidstr), "%ld\n", pid);
    ret = 0;
    written = 0;
    
    while (remaining > 0) {
        ret = write(fd, pidstr + written, remaining);
        if (ret != -1) {
            remaining -= ret;
            written += ret;
        } else if (errno == EINTR) {
            continue;
        } else {
            return ret;
        }
    }

    if (close(fd) == -1) {
        return -1;
    }

    return ret;
}


static BOOL
Unlock(const char *lockfile)
{
    if (unlink(lockfile) == -1) {
        fprintf(stderr, _("bongo-manager: couldn't unlink %s\n"), LOCKFILE);
        return FALSE;
    }
    
    return TRUE;
}

static pid_t
ReadPid(const char *filename)
{
    FILE *f;
    char pidstr[20];
    pid_t ret = -1;
    
    f = fopen(filename, "r");
    
    if (f) {
        if (fgets(pidstr, sizeof(pidstr), f)) {
            ret = (pid_t)strtol(pidstr, NULL, 10);
        }
        fclose(f);
    } else {
        return -1;
    }
    
    return ret;
}

static void
WaitOnPidFile(void)
{
    struct stat buf;
    
    while (stat(LOCKFILE, &buf) != -1) {
        XplDelay(500);
    }   
}

static void
ShowHelp(void) 
{
    printf(_("Usage: bongo-manager [OPTIONS]\n"
           "Starts and manages Bongo processes.\n\n"
           "Normal options:\n"
           "\t-d: Run in background after starting agents.\n"
           "\t-k: Keep agents alive, restarting them after crashes.\n"
           "\t-f: Force start, replacing " LOCKFILE ".\n"
           "\t-s: Shut down an already-running bongo-manager.\n"
           "\t-r: Ask a running bongo-manager to restart stopped agents.\n"
           "Managed-slapd options:\n"
           "\t-l: Only start the slapd server.\n"
           "\t-e: Kill existing slapd server.\n")
        );
    
}

static BOOL 
ParseArgs(int argc, char **argv, 
          BOOL *daemonize,
          BOOL *force, 
          BOOL *keepAlive,
          BOOL *shutdown,
          BOOL *reload,
          BOOL *slapdOnly,
          BOOL *killExistingSlapd)
{
    int arg;
    
    *daemonize = *force = *keepAlive = *shutdown = *reload = *slapdOnly = *killExistingSlapd = FALSE;

    while ((arg = getopt(argc, argv, "dfksrhle")) != -1) {
        switch(arg) {
        case 'd':
            *daemonize = TRUE;
            break;
        case 'f' :
            *force = TRUE;
            break;
        case 'k' :
            *keepAlive = TRUE;
            break;
        case 's' :
            *shutdown = TRUE;
            break;
        case 'r' :
            *reload = TRUE;
            break;
        case 'l' :
            *slapdOnly = TRUE;
            break;
        case 'e' :
            *killExistingSlapd = TRUE;
            break;
        case 'h' :
        case '?' :
            return FALSE;
        }    
    }

    if (*shutdown && (*daemonize || *force || *reload || *keepAlive || *slapdOnly || *killExistingSlapd)) {
        fprintf(stderr, _("%s: -s cannot be supplied with other options.\n"), argv[0]);
        return FALSE;
    }

    if (*reload && (*daemonize || *force || *keepAlive || *slapdOnly || *killExistingSlapd)) {
        fprintf(stderr, _("%s: -r cannot be supplied with other options.\n"), argv[0]);
        return FALSE;
    }   

    if (*slapdOnly && (*keepAlive)) {
        fprintf(stderr, _("%s: -l cannot be supplied with other options.\n"), argv[0]);
        return FALSE;
    }

    return TRUE;
}

static int
StartSlapd(BOOL killExisting)
{
    int status = 0;
    int err;
    char buf[MDB_MAX_OBJECT_CHARS + 1];
    char url[MDB_MAX_OBJECT_CHARS + 1];

    char *host = "127.0.0.1";
    int port = 0;
    pid_t pid = 0;

    int sockfd = 0;
    struct sockaddr_in serv_addr;
    struct hostent* host_info;
    long host_addr;

    /* Get rid of an existing managed-slapd processs */
    pid = ReadPid(SLAPD_LOCKFILE);
    
    if (pid > 0) {
        if (kill(pid, 0) == 0 || errno != ESRCH) {
            /* Existing process */
            if (killExisting) {
                if (kill(pid, SIGKILL) == 0) {
                    XplDelay(1000);
                    fprintf(stderr, _("bongo-manager: killed existing managed-slapd process.\n"));
                } else if (errno != ESRCH) {
                    fprintf(stderr, _("bongo-manager: could not kill existing managed-slapd process.\n"));
                    return 1;
                }
            } else {
                fprintf(stderr, _("bongo-manager: managed-slapd appears to be running as pid %ld\n"), pid);
                fprintf(stderr, _("bongo-manager: if this is definitely the bongo slapd, you can run with -e to kill it\n"));
                return 1;
            }
        }
    }

    /* read config */

    if (!MsgGetConfigProperty((unsigned char *) buf,
	   (unsigned char *) MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PORT)) {
        fprintf(stderr, _("bongo-manager: error reading managed slapd port from config file.\n"));
	return 1;
    }

    port = atoi(buf);

    if (!MsgGetConfigProperty((unsigned char *) buf,
	   (unsigned char *) MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PATH)) {
	fprintf(stderr, _("Error reading managed slapd path from config file.\n"));
	return 1;
    }

    snprintf(url, MDB_MAX_OBJECT_CHARS, "ldap://%s:%d", host, port);

    printf(_("bongo-manager: starting managed slapd...\n"));

    pid = fork();

    if (!pid) {
        /* We would pass the -u argument to slapd to set its uid here,
           but at this point in bongo-manager we've already dropped
           privs to BONGO_USER. */
        execl(buf, buf,
              "-f", XPL_DEFAULT_CONF_DIR "/bongo-slapd.conf",
              "-h", url,
              "-n", "bongo-slapd",
              "-s", "0",
              NULL);

        exit(1);
    }

    waitpid(pid, &status, 0);

    if (!WIFEXITED(status)) {
        /* slapd process didn't exit normally */
        return 1;
    } else if (WEXITSTATUS(status)) {
        /* slapd process exited with an error */
        return 1;
    }

    /* wait until the slapd process is reachable */
    sockfd = socket(AF_INET, SOCK_STREAM, 0);

    if (sockfd == -1) {
        fprintf(stderr, _("Error creating socket: %s\n"), strerror(errno));
        return 1;
    }

    host_info = gethostbyname(host);

    memcpy(&host_addr, host_info->h_addr, host_info->h_length);
    serv_addr.sin_addr.s_addr = host_addr;
    serv_addr.sin_family = AF_INET;
    serv_addr.sin_port = htons(port);

    while (1) {
        err= connect(sockfd, (struct sockaddr *)&serv_addr, sizeof(serv_addr));
        if (err == -1) {
            int error = errno;

            if (error == ECONNREFUSED) {
                sleep(1);
                continue;
            } else {
                fprintf(stderr, _("bongo-manager: error connecting to slapd: %s\n"), strerror(error));
                return 1;
            }
        }

        SlapdPID = ReadPid(SLAPD_LOCKFILE);
        printf(_("bongo-manager: slapd started\n"));
        break;
    }
    close(sockfd);

    return err;
}

static int
StopSlapd(void)
{
    int err = 0;

    if (kill(SlapdPID, SIGTERM) != 0) {
        unlink(SLAPD_LOCKFILE);
        err = 0;
    } else {
        err = 1;
    }

    return err;
}

static void
ParentSignalHandler(int signo)
{
    switch (signo) {
    case SIGCHLD:
        ChildError = TRUE;
    case SIGUSR1:
        Exiting = TRUE;
    } 
}

static int
Daemonize(void)
{
    pid_t pid;

    /* these have to be before the fork, otherwise the child could
     * signal before the parent has set them */
    signal(SIGUSR1, ParentSignalHandler);
    signal(SIGCHLD, ParentSignalHandler);

    pid = fork();

    if (pid < 0) {
        fprintf(stderr, _("bongo-manager: could not fork into background (%d): %s\n"),
               errno, strerror(errno));
        return 1;
    } else if (pid != 0) {
        /* wait for child to signal us that agents have been started, or it had an error */
        while (!Exiting) {
            XplDelay(1000);
        }
        if (ChildError) {
            int status;
            waitpid(pid, &status, 0);
            exit(WEXITSTATUS(status));
        }
        printf(_("bongo-manager: running in background\n"));
        _exit(0);
    }

    signal(SIGUSR1, SIG_DFL);
    signal(SIGCHLD, SIG_DFL);

    setsid();
    chdir("/");
    umask(0);

    return 0;
}

static int
SignalParent()
{
    return kill (getppid(), SIGUSR1);
}

int
main(int argc, char **argv)
{
    int numWaiting;
    int lastNumWaiting;
    int forceShutdownTime;
    BOOL daemonize;
    BOOL force;
    BOOL keepAlive;
    BOOL shutdown;
    BOOL reload;
    BOOL slapdOnly;
    BOOL killExistingSlapd;
    char buf[CONN_BUFSIZE];
    BOOL droppedPrivs = FALSE;
    BOOL unlockFile = FALSE;
    BOOL startLdap = FALSE;

    XplInit();

    XplSaveRandomSeed();	/// is this appropriate?

    if (getuid() == 0) {
        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            fprintf(stderr, _("bongo-manager: could not drop to unprivileged user '%s'\n"), MsgGetUnprivilegedUser());
            exit(1);
        }
        droppedPrivs = TRUE;
    }

    if (!ParseArgs(argc, argv, &daemonize, &force, &keepAlive, &shutdown, &reload, &slapdOnly, &killExistingSlapd)) {
        ShowHelp();
        exit(1);
    }

    if (!droppedPrivs) {
        fprintf(stderr, _("bongo-manager: must be run by root\n"));
        exit(1);
    }

    if (reload) {
        pid_t pid = ReadPid(LOCKFILE);

        if (pid <= 0) {
            fprintf(stderr, _("bongo-manager: could not open pid file '%s'\n"), LOCKFILE);
            exit(1);
        }

        if (kill(pid, SIGUSR1) == -1) {
            if (errno == ESRCH) {
                fprintf (stderr, _("bongo-manager: bongo-manager does not appear to be running.\n"));
            } else {
                fprintf (stderr, _("bongo-manager: unable to reload services: %s\n"), strerror(errno));
            }
            exit(1);
        }
        exit(0);
    }

    if (shutdown) {
        pid_t pid = ReadPid(LOCKFILE);
        if (pid <= 0) {
            fprintf(stderr, _("bongo-manager: could not open pid file '%s'\n"), LOCKFILE);
            exit(1);
        }

        if (kill(pid, SIGTERM) == -1) {
            if (errno == ESRCH) {
                fprintf (stderr, _("bongo-manager: bongo-manager does not appear to be running.\n"));
            } else {
                fprintf (stderr, _("bongo-manager: unable to shut down services: %s\n"), strerror(errno));
            }
            exit(1);
        }

        WaitOnPidFile();
        exit(0);
    }

    if (daemonize) {
        if (Daemonize()) {
            exit(1);
        }
    }

get_lock:
    if (Lock(force) == -1) {
        if (errno == EEXIST) {
            pid_t pid = ReadPid(LOCKFILE);
            if (kill(pid, 0) != -1 || errno != ESRCH) {
                fprintf(stderr, _("bongo-manager: another bongo-manager process appears to be running.\n"));
                fprintf(stderr, _("bongo-manager: run with -s to stop an existing process, or -f to ignore the existing pidfile.\n"));
            } else if (!force) {
                fprintf(stderr, _("bongo-manager: removing stale lock file in %s.\n"), LOCKFILE);
                force = TRUE;
                goto get_lock;
            } else {
                fprintf (stderr, _("bongo-manager: could not remove stale lock file in %s.\n"), LOCKFILE);
            }
        } else if (errno == EPERM) {
            fprintf(stderr, _("bongo-manager: %s user does not have permission to create a lock file in %s\n"),
                   MsgGetUnprivilegedUser(), XPL_DEFAULT_WORK_DIR);
        } else {
            fprintf(stderr, _("bongo-manager: could not create lock file in %s : %s\n"), XPL_DEFAULT_WORK_DIR, strerror(errno));
        }

        goto err_handler;
    }
    unlockFile = TRUE;

    if (!MemoryManagerOpen("bongomanager")) {
        fprintf(stderr, _("bongo-manager: failed to initialize memory manager.  Exiting\n"));
        goto err_handler;
    }
    ConnStartup(DEFAULT_CONNECTION_TIMEOUT, TRUE);

    if (!MsgGetConfigProperty(ServerDN, MSGSRV_CONFIG_PROP_MESSAGING_SERVER)) {
        fprintf(stderr, _("bongo-manager: Couldn't read the server DN from bongo.conf.\n"));
        goto err_handler;
    }

    if (MsgGetConfigProperty((unsigned char *) buf, 
                             (unsigned char *) MSGSRV_CONFIG_PROP_MANAGED_SLAPD)) {
        startLdap = atoi(buf);
    }

    if (startLdap) {
        int err;

        err = StartSlapd(killExistingSlapd);

        if (err != 0) {
            fprintf(stderr, _("bongo-manager: managed slapd process failed to start.\n"));
            goto err_handler;
        }
    } else if (slapdOnly) {
        fprintf(stderr, _("bongo-manager: -l only works with a managed-slapd database.\n"));
        goto err_handler;
    }

    if (!slapdOnly) {
        if (!MDBInit()) {
            fprintf(stderr, _("bongo-manager: unable to intialize directory access.\n"));
            goto err_handler;
        }

	if (!MsgInit()) {
	    fprintf(stderr, _("bongo-manager: unable to MsgInit()\n"));
	    goto err_handler;
	}
	NMAPInitialize();

        DirectoryHandle = MsgGetSystemDirectoryHandle();
        if (DirectoryHandle == NULL) {
            fprintf(stderr, _("bongo-manager: unable to initialize messaging library.\n"));
            goto err_handler;
        }
    }

    signal(SIGTERM, SignalHandler);
    signal(SIGINT, SignalHandler);
    signal(SIGCHLD, SignalHandler);
    signal(SIGUSR1, SignalHandler);
    
    if (!slapdOnly) {
        SetupAgentList();
        StartStore();
        XplDelay(1000);	// hack: small delay to let store start up
        if (!LoadAgentConfiguration()) {
            printf(_("bongo-manager: Couldn't load configuration for agents\n"));
        }
        StartAgents(FALSE, FALSE);
    }

    if (daemonize) {
        SignalParent();
    }

    while(!Exiting) {
        XplDelay(10000); // existing hack : startup again?
        if (!slapdOnly) {
            Reap();
            
            if (ReloadNow) {
                ResetCrashiness();
            }
            
            if ((ReloadNow || keepAlive) && !Exiting) {
                StartAgents(!ReloadNow, TRUE);
            }
            ReloadNow = FALSE;
        }
    }

    printf(_("bongo-manager: Shutting down...\n"));

    if (!slapdOnly) {
        numWaiting = lastNumWaiting = 0;
        
        forceShutdownTime = time(NULL) + 10;
        
        while ((numWaiting = AgentsStillRunning()) > 0) {
            lastNumWaiting = numWaiting;
            
            if (forceShutdownTime < time(NULL)) {
                fprintf(stderr, "bongo-manager: '");
                BlameAgents();
                fprintf(stderr, _("' stubbornly refusing to die, insisting.\n"));
                killpg(LeaderPID, SIGKILL);
                forceShutdownTime = time(NULL) + 5;
            }
            
            XplDelay(500);
            
            Reap();
        }
    }
    
    Unlock(LOCKFILE);
    
    if (SlapdPID > 0) {
        StopSlapd();
    }    

    printf(_("bongo-manager: shutdown complete.\n"));

    return 0;

err_handler:
    if (SlapdPID > 0) {
        StopSlapd();
    }
    if (unlockFile) {
        Unlock(LOCKFILE);
    }
    exit(1);
}
