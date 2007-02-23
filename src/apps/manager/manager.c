#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <msgapi.h>
#include <sys/wait.h>
#include <sys/resource.h>
#include <time.h>
#include <unistd.h>
#include <getopt.h>

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

/* FIXME: these shouldn't be hard-coded */
static const BongoAgentSpec AgentSpecs[] = {
    { "bongodmc", MSGSRV_AGENT_DMC, 0, FALSE },
    { "bongostore", MSGSRV_AGENT_STORE, 2, FALSE },
    { "bongoqueue", MSGSRV_AGENT_QUEUE, 3, FALSE },
    { "bongoantispam", MSGSRV_AGENT_ANTISPAM, 3, FALSE },
    { "bongoavirus", MSGSRV_AGENT_ANTIVIRUS, 3, FALSE },
    { "bongocollector", MSGSRV_AGENT_COLLECTOR, 5, FALSE },
    { "bongomailprox", MSGSRV_AGENT_PROXY, 5, FALSE },
    { "bongoconnmgr", MSGSRV_AGENT_CONNMGR, 2, FALSE },
    { "bongopluspack", PLUSPACK_AGENT, 4, FALSE },
    { "bongorules", MSGSRV_AGENT_RULESRV, 3, FALSE },
    { "bongosmtp", MSGSRV_AGENT_SMTP, 3, FALSE },
    { "bongoimap", MSGSRV_AGENT_IMAP, 3, FALSE },
    { "bongopop3", MSGSRV_AGENT_POP, 3, FALSE },
    { "bongocalcmd", MSGSRV_AGENT_CALCMD, 3, FALSE },
};

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

static BongoAgent *
FindAgentByDn(const char *dn)
{
    unsigned int i;
    
    for (i = 0; i < AllAgents->len; i++) {
        BongoAgent *agent = &BongoArrayIndex(AllAgents, BongoAgent, i);
        if (!XplStrCaseCmp(agent->spec.dn, dn)) {
            return agent;
        }
    }

    return NULL;
}

static BOOL
ProcessAgentList(MDBValueStruct *list,
                 MDBValueStruct *v)
{
    unsigned long i;
    BOOL result = TRUE;

    for (i = 0; i < list->Used; i++) {
        BongoAgent *agent;
        char *p;

        p = strrchr(list->Value[i], '\\');
        if (!p) {
            continue;
        }

        /* FIXME: should check the type of the node and see if it's a
         * BongoAgent.  For now, special case a node we know isn't a
         * BongoAgent */
        if (!XplStrCaseCmp(p + 1, "User Settings")) {
            continue;
        }

        agent = FindAgentByDn(p + 1);
        
        if (!agent) {
            fprintf(stderr, "bongomanager: Unknown agent '%s'\n", p + 1);
            continue;
        }

        if ((MDBRead(list->Value[i], MSGSRV_A_DISABLED, v) == 0) || (v->Value[0][0] != '1')) {
            agent->enabled = TRUE;
            MDBFreeValues(v);
        }
    }

    return result;
}

static BOOL
CheckAgentConfiguration(void)
{
    MDBValueStruct *list;
    MDBValueStruct *config;
    BOOL result;
    
    list = MDBCreateValueStruct(DirectoryHandle, NULL);
    
    if (!list) {
        fprintf(stderr, "bongomanager: could not create value struct\n");
        return FALSE;
    }
    
    config = MDBCreateValueStruct(DirectoryHandle, NULL);
    if (!config) {
        fprintf(stderr, "bongomanager: could not create value struct\n");
        MDBDestroyValueStruct(list);
        return FALSE;
    }

    result = MDBEnumerateObjects(ServerDN,
                                 NULL,
                                 NULL,
                                 list);
    if (result && list->Used) {
        result = ProcessAgentList(list, config);
    } else {
        result = FALSE;
    }

    MDBFreeValues(list);
    MDBDestroyValueStruct(list);
    MDBDestroyValueStruct(config);

    return result;
}

static BOOL
GetAgents(void)
{
    unsigned int i;
    unsigned int numSpecs = sizeof(AgentSpecs) / sizeof(BongoAgentSpec);

    AllAgents = BongoArrayNew(sizeof(BongoAgent), numSpecs);
    
    for (i = 0; i < numSpecs; i++) {
        BongoAgent agent = {{0,} };
        agent.spec = AgentSpecs[i];
        agent.enabled = FALSE; /* Will be enabled by CheckAgentConfiguration */
        if (agent.spec.priority > HighestPriority) {
            HighestPriority = agent.spec.priority;
        }
        
        BongoArrayAppendValue(AllAgents, agent);
    }

    return CheckAgentConfiguration();
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
                fprintf(stderr, "bongomanager: %s terminated with signal %d.\n", 
                       agent->spec.program, WTERMSIG(status));
                if (WTERMSIG(status) != SIGTERM) {
                    agent->crashed = TRUE;
                }
            } else if (!Exiting) {
                fprintf(stderr, "bongomanager: %s exited\n", agent->spec.program);
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
            fprintf(stderr, "couldn't set rlimit: %s\n", strerror(errno));
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
        fprintf(stderr, "failed to exec %s (%s)\n", args[0], strerror(errno));
        exit(1);
    } else { /* parent */
        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            fprintf(stderr, "bongomanager: Could not drop to unprivileged user '%s', Exiting.\n",
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
        fprintf(stderr, "bongomanager: %s has crashed %d times within %d seconds of each other, not restarting.\n", 
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
                printf("bongomanager: starting %s\n", agent->spec.program);
            }
            StartAgent(agent);
        }
    }

    return WaitForCallbacks();
}

static void
StartAgents(BOOL onlyCrashed, BOOL printMessage)
{
    int i;
    
    for (i = 0; i <= HighestPriority; i++) {
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
    char pid[20];
    int fd;
    int remaining;
    int written;
    int ret;
    int mode;
    
    mode = O_CREAT | O_WRONLY;
    if (!force) {
        mode |= O_EXCL;
    }

    fd = open(LOCKFILE, mode, S_IREAD | S_IWRITE);
    if (fd == -1) {
        return -1;
    }

    remaining = snprintf(pid, sizeof(pid), "%d\n", getpid());
    if (remaining > (int)sizeof(pid)) {
        fprintf(stderr, "bongomanager: unlikely pid, Exiting\n");
        exit(1);
    }

    ret = 0;
    written = 0;
    
    while (remaining > 0) {
        ret = write(fd, pid + written, remaining);
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
        fprintf(stderr, "bongomanager: couldn't unlink %s\n", LOCKFILE);
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
    printf("Usage: bongomanager [OPTIONS]\n"
           "Starts and manages Bongo processes.\n\n"
           "Normal options:\n"
           "\t-d: Run in background after starting agents.\n"
           "\t-k: Keep agents alive, restarting them after crashes.\n"
           "\t-f: Force start, replacing " LOCKFILE ".\n"
           "\t-s: Shut down an already-running bongomanager.\n"
           "\t-r: Ask a running bongomanager to restart stopped agents.\n"
           "Managed-slapd options:\n"
           "\t-l: Only start the slapd server.\n"
           "\t-e: Kill existing slapd server.\n"
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
        fprintf(stderr, "%s: -s cannot be supplied with other options.\n", argv[0]);
        return FALSE;
    }

    if (*reload && (*daemonize || *force || *keepAlive || *slapdOnly || *killExistingSlapd)) {
        fprintf(stderr, "%s: -r cannot be supplied with other options.\n", argv[0]);
        return FALSE;
    }   

    if (*slapdOnly && (*keepAlive)) {
        fprintf(stderr, "%s: -l cannot be supplied with other options.\n", argv[0]);
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
                    fprintf(stderr, "bongomanager: killed existing managed-slapd process.\n");
                } else if (errno != ESRCH) {
                    fprintf(stderr, "bongomanager: could not kill existing managed-slapd process.\n");
                    return 1;
                }
            } else {
                fprintf(stderr, "bongomanager: managed-slapd appears to be running as pid %d\n"
                       "bongomanager: if this is definitely the bongo slapd, you can run with -e to kill it\n",
                       pid);
                return 1;
            }
        }
    }

    /* read config */

    if (!MsgGetConfigProperty((unsigned char *) buf,
	   (unsigned char *) MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PORT)) {
        fprintf(stderr, "bongomanager: error reading managed slapd port from config file.\n\r");
	return 1;
    }

    port = atoi(buf);

    if (!MsgGetConfigProperty((unsigned char *) buf,
	   (unsigned char *) MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PATH)) {
	fprintf(stderr, "Error reading managed slapd path from config file.\n\r");
	return 1;
    }

    snprintf(url, MDB_MAX_OBJECT_CHARS, "ldap://%s:%d", host, port);

    printf("bongomanager: starting managed slapd...\n");

    pid = fork();

    if (!pid) {
        /* We would pass the -u argument to slapd to set its uid here,
           but at this point in bongomanager we've already dropped
           privs to BONGO_USER. */
        execl(buf, buf,
              "-f", XPL_DEFAULT_CONF_DIR "/bongo-slapd.conf",
              "-h", url,
              "-n", "bongo-slapd",
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
        fprintf(stderr, "Error creating socket: %s\n", strerror(errno));
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
                fprintf(stderr, "bongomanager: error connecting to slapd: %s\n", strerror(error));
                return 1;
            }
        }

        SlapdPID = ReadPid(SLAPD_LOCKFILE);
        printf("bongomanager: slapd started\n");
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
        fprintf(stderr, "bongomanager: could not fork into background (%d): %s\n",
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
        printf("bongomanager: running in background\n");
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
            fprintf(stderr, "bongomanager: could not drop to unprivileged user '%s'\n", MsgGetUnprivilegedUser());
            exit(1);
        }
        droppedPrivs = TRUE;
    }

    if (!ParseArgs(argc, argv, &daemonize, &force, &keepAlive, &shutdown, &reload, &slapdOnly, &killExistingSlapd)) {
        ShowHelp();
        exit(1);
    }

    if (!droppedPrivs) {
        fprintf(stderr, "bongomanager: must be run by root\n");
        exit(1);
    }

    if (reload) {
        pid_t pid = ReadPid(LOCKFILE);

        if (pid <= 0) {
            fprintf(stderr, "bongomanager: could not open pid file '%s'\n", LOCKFILE);
            exit(1);
        }

        if (kill(pid, SIGUSR1) == -1) {
            if (errno == ESRCH) {
                fprintf (stderr, "bongomanager: bongomanager does not appear to be running.\n");
            } else {
                fprintf (stderr, "bongomanager: unable to reload services: %s\n", strerror(errno));
            }
            exit(1);
        }
        exit(0);
    }

    if (shutdown) {
        pid_t pid = ReadPid(LOCKFILE);
        if (pid <= 0) {
            fprintf(stderr, "bongomanager: could not open pid file '%s'\n", LOCKFILE);
            exit(1);
        }

        if (kill(pid, SIGTERM) == -1) {
            if (errno == ESRCH) {
                fprintf (stderr, "bongomanager: bongomanager does not appear to be running.\n");
            } else {
                fprintf (stderr, "bongomanager: unable to shut down services: %s\n", strerror(errno));
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
                fprintf(stderr, "bongomanager: another bongomanager process appears to be running.\n");
                fprintf(stderr, "bongomanager: run with -s to stop an existing process, or -f to ignore the existing pidfile.\n");
            } else if (!force) {
                fprintf(stderr, "bongomanager: removing stale lock file in %s.\n", LOCKFILE);
                force = TRUE;
                goto get_lock;
            } else {
                fprintf (stderr, "bongomanager: could not remove stale lock file in %s.\n", LOCKFILE);
            }
        } else if (errno == EPERM) {
            fprintf(stderr, "bongomanager: %s user does not have permission to create a lock file in %s\n",
                   MsgGetUnprivilegedUser(), XPL_DEFAULT_WORK_DIR);
        } else {
            fprintf(stderr, "bongomanager: could not create lock file in %s : %s\n", XPL_DEFAULT_WORK_DIR, strerror(errno));
        }

        goto err_handler;
    }
    unlockFile = TRUE;

    if (!MemoryManagerOpen("bongomanager")) {
        fprintf(stderr, "bongomanager: failed to initialize memory manager.  Exiting\n");
        goto err_handler;
    }

    if (!MsgGetConfigProperty(ServerDN, MSGSRV_CONFIG_PROP_MESSAGING_SERVER)) {
        fprintf(stderr, "bongomanager: Couldn't read the server DN from bongo.conf.\r\n");
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
            fprintf(stderr, "bongomanager: managed slapd process failed to start.\n");
            goto err_handler;
        }
    } else if (slapdOnly) {
        fprintf(stderr, "bongomanager: -l only works with a managed-slapd database.\n");
        goto err_handler;
    }

    if (!slapdOnly) {
        if (!MDBInit()) {
            fprintf(stderr, "bongomanager: unable to intialize directory access.\n");
            goto err_handler;
        }

        DirectoryHandle = MsgGetSystemDirectoryHandle();
        if (DirectoryHandle == NULL) {
            fprintf(stderr, "bongomanager: unable to initialize messaging library.\n");
            goto err_handler;
        }
        
        if (!GetAgents()) {
            fprintf(stderr, "bongomanager: no configured agents.\n");
            goto err_handler;
        }
    }

    signal(SIGTERM, SignalHandler);
    signal(SIGINT, SignalHandler);
    signal(SIGCHLD, SignalHandler);
    signal(SIGUSR1, SignalHandler);

    if (!slapdOnly) {
        StartAgents(FALSE, FALSE);
    }

    if (daemonize) {
        SignalParent();
    }

    while(!Exiting) {
        XplDelay(10000);
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

    printf("bongomanager: shutting down...\n");    

    if (!slapdOnly) {
        numWaiting = lastNumWaiting = 0;
        
        forceShutdownTime = time(NULL) + 10;
        
        while ((numWaiting = AgentsStillRunning()) > 0) {
            lastNumWaiting = numWaiting;
            
            if (forceShutdownTime < time(NULL)) {
                fprintf(stderr, "bongomanager: '");
                BlameAgents();
                fprintf(stderr, "' stubbornly refusing to die, insisting.\n");
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

    printf("bongomanager: shutdown complete.\n");

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
