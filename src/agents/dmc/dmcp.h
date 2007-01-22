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

#ifndef DMCP_H
#define DMCP_H

#define DMC_COMMAND_AGENT "AGENT"
#define DMC_COMMAND_AUTH "AUTH"
#define DMC_COMMAND_CMD "CMD"
#define DMC_COMMAND_DEREGISTER "DEREGISTER"
#define DMC_COMMAND_GET "GET"
#define DMC_COMMAND_HELP "HELP"
#define DMC_COMMAND_HEARTBEAT "HEARTBEAT"
#define DMC_COMMAND_LOGIN "LOGIN"
#define DMC_COMMAND_PROMPT "PROMPT"
#define DMC_COMMAND_QUIT "QUIT"
#define DMC_COMMAND_RESET "RESET"
#define DMC_COMMAND_REGISTER "REGISTER"
#define DMC_COMMAND_RESTART "RESTART"
#define DMC_COMMAND_SERVER "SERVER"
#define DMC_COMMAND_SET "SET"
#define DMC_COMMAND_SHUTDOWN "SHUTDOWN"
#define DMC_COMMAND_START "START"
#define DMC_COMMAND_STAT0 "STAT0"
#define DMC_COMMAND_STAT1 "STAT1"
#define DMC_COMMAND_STAT2 "STAT2"
#define DMC_COMMAND_STATS "STATS"

#define DMC_HELP_NOT_AVAILABLE "No help defined.\r\n"
#define DMC_HELP_COMMANDS "Commands:\r\n  AGENT       DEREGISTER  GET         HELP        LOGIN       PROMPT\r\n  QUIT        REGISTER    RESET       RESTART     SERVER      SET\r\n  VERSION\r\n"
#define DMC_HELP_AGENT "AGENT [\"<AgentName>\"]\r\nThe AGENT command, without the optional <AgentName> argument is used to\r\nidentify the NetMail agents that are manageable on the current server.\r\nThe AGENT command, with the optional double-quote enclosed <AgentName> argument\r\nis used to select the agent to be managed.\r\nThe AGENT command is only useable after using the SERVER command to select a\r\nNetMail messaging server to be managed.  After using the AGENT command to\r\nselect a manageable agent; use the RESET command to de-select the agent.\r\n"
#define DMC_HELP_AUTH "AUTH <Hash> - Authenticates hosts using an MD5 hash.\r\n"
#define DMC_HELP_CMD "CMD\r\nThe CMD command, without any arguments, will return a list of available\r\nNetMail agent commands.\r\nThe CMD command, with an optional NetMail agent command will return\r\ncommand specific responses.  Use 'CMD HELP <AgentCommand>' to determine\r\nspecific command response values.\r\nThe CMD command is only available after using the AGENT command to select a\r\nNetMail agent to be managed.\r\n"
#define DMC_HELP_DEREGISTER "DEREGISTER <Signature>\r\nThe DEREGISTER command is used to remove a NetMail agent from the server's\r\nmanageable list.  The <Signature> argument is the 32 character signature\r\nreturned to the registering agent from the REGISTER command.\r\nThe DEREGISTER command may only be used after establishing a connection with\r\nthe NetMail server's distributed management console and may only be issued\r\nby the NetMail agent that issued the REGISTER command.\r\n"
#define DMC_HELP_GET "GET [\"<VarName>\"]\r\nThe GET command, without any arguments, will return a list of manageable\r\nvariables for the currently selected NetMail agent.\r\nThe GET command with a double-quote enclosed variable names will\r\nreturn the value of the specified NetMail agent manageable variable.\r\nThe GET command is only available after using the AGENT command to select a\r\nNetMail agent to be managed.\r\n"
#define DMC_HELP_HELP "HELP [<Command>]\r\nThe HELP command, without any arguments, will return the NetMail Distributed\r\nManagement Console version and commands.\r\n"
#define DMC_HELP_LOGIN "LOGIN <Username> <Password>\r\nThe LOGIN command is used to provide credentials to the NetMail Distributed\r\nManagement Console.\r\n"
#define DMC_HELP_PROMPT "PROMPT\r\nThe PROMPT command is used to toggle the state of the NetMail Distributed\r\nManagement Console prompt.  The prompt is returned after each command is\r\nprocessed.\r\n"
#define DMC_HELP_QUIT "QUIT\r\nThe QUIT command is used to terminate the NetMail Distributed Management\r\nConsole session.\r\n"
#define DMC_HELP_RESET "RESET\r\nThe RESET command is used to de-select the currently selected managed\r\nresource.\r\nThe RESET command, when used after using the AGENT command to select a\r\nmanageable NetMail agent, allows the session to select another manageable\r\nNetMail agent on the current server.\r\nThe RESET command, when used after using the SERVER command to select a\r\nmanageable NetMail server, allows the session to select another manageable\r\nNetMail server.\r\n"
#define DMC_HELP_REGISTER "REGISTER \"<Identity>\" <Port> <SSL Port> <Seed>\r\nThe REGISTER command is used to register a manageable NetMail agent with the\r\nNetMail Distributed Management Console.  The double-quote enclosed <Identity>\r\nargument is the directory name of the agent being registered.  The <Port>\r\nargument identifies the dynamic port on which the NetMail agent is listening\r\nfor clear text requests from the NetMail Distributed Management Console\r\nserver.\r\nThe <SSLPort> argument identifies the dynamic port on which the NetMail agent\r\nis listening for SSL encrypted requests from the NetMail Distributed Management\r\nConsole server.\r\nThe <Seed> argument is an ASCII string that will be used when generating the\r\nregistration signature that will be returned upon successful registration.\r\nEither port argument can be passed as -1 to disable either clear text or\r\nencrypted management requests.\r\nUpon successful completion of the registration request, the NetMail Distributed\r\nManagement Console will return an ASCII string containing 32 characters which\r\n\r\nmust be used by the NetMail agent when using the DEREGISTER command.\r\n"
#define DMC_HELP_RESTART "RESTART\r\nThe RESTART command is used to stop and then re-start the currently managed\r\nresource.  Issuing the command, while managing a NetMail messaging server,\r\nwill stop and re-start all NetMail agents on the managed server.  Issuing the\r\ncommand, while managing a NetMail agent, will stop and re-start only the\r\nmanaged agent.\r\n"
#define DMC_HELP_SERVER "SERVER [\"<ServerName>\"]\r\nThe SERVER command, without the optional <ServerName> argument is used to\r\nidentify the NetMail messaging server that are manageable.\r\nThe SERVER command, with the optional double-quote enclosed <ServerName>\r\nargument is used to select a messaging server to be managed.  The session\r\nuser must be configured as a manager of the messaging server to be selected.\r\nThe SERVER command is only useable after authenticating the session using the\r\nLOGIN command.\r\n"
#define DMC_HELP_SET "SET \"<VarName>\" <DataLength>\r\nThe SET command is used to change the value of a managed agent's variable as\r\nidentified with the double-quote enclosed variable name argument.  The\r\n<DataLength> argument states the size of the character string representing\r\nthe new value for the variable.  The NetMail Distributed Management Console\r\nwill verify that the specified variable can be written to and then return\r\na success string requesting that the character string be sent.\r\n"

#define DMC_AGENT_DMC_PRIORITY        0
#define DMC_AGENT_NMAP_PRIORITY       1
#define DMC_AGENT_QUEUE_PRIORITY      3
#define DMC_AGENT_SMTP_PRIORITY       5
#define DMC_AGENT_MINIMUM_PRIORITY    10
#define DMC_AGENT_DEFAULT_PRIORITY    65535

typedef enum _DMCStates {
    DMC_STARTING = 0, 
    DMC_INITIALIZING, 
    DMC_LOADING, 
    DMC_RUNNING, 
    DMC_RELOADING, 
    DMC_UNLOADING, 
    DMC_STOPPING, 
    DMC_DONE, 

    DMC_MAX_STATES
} DMCStates;

typedef enum _DMCReceiverStates {
    DMC_RECEIVER_RUNNING = 0, 
    DMC_RECEIVER_SHUTTING_DOWN, 
    DMC_RECEIVER_DISABLED, 
    DMC_RECEIVER_CONNECTION_LIMIT, 
    DMC_RECEIVER_OUT_OF_MEMORY, 

    DMC_RECEIVER_MAX_STATES
} DMCReceiverStates;

typedef enum _DMCClientStates {
    CLIENT_STATE_CLOSING = 0, 
    CLIENT_STATE_FRESH, 
    CLIENT_STATE_AUTHENTICATED, 
    CLIENT_STATE_SERVER, 
    CLIENT_STATE_AGENT, 

    CLIENT_STATE_MAX
} DMCClientStates;

typedef enum _DMCClientFlags {
    CLIENT_FLAG_PROMPT = (1 << 0), 
    CLIENT_FLAG_CONNECTED = (1 << 1)
} DMCClientFlags;

typedef enum _DMCVariableFlags {
    VARIABLE_FLAG_WRITEABLE = (1 << 0)
} DMCVariableFlags;

typedef enum _DMCAgentFlags {
    AGENT_FLAG_REGISTERED = (1 << 0), 
    AGENT_FLAG_CLEARTEXT = (1 << 1), 
    AGENT_FLAG_SECURE = (1 << 2)
} DMCAgentFlags;

typedef enum _DMCServerFlags {
    SERVER_FLAG_REGISTERED = (1 << 0), 
    SERVER_FLAG_CLEARTEXT = (1 << 1), 
    SERVER_FLAG_SECURE = (1 << 2),
    SERVER_FLAG_LOCAL = (1 << 3)
} DMCServerFlags;

typedef struct _ManagedVariable {
    DMCVariableFlags flags;

    unsigned char *name;
} ManagedVariable;

typedef struct _ManagedAgent {
    DMCAgentFlags flags;

    struct {
        struct sockaddr_in clear;
        struct sockaddr_in ssl;
    } address;

    struct {
        unsigned long count;
        ManagedVariable *list;
    } variable;

    struct {
        unsigned long count;
        unsigned char **name;
    } command;

    unsigned long rdnOffset;

    unsigned char identity[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char signature[36];
} ManagedAgent;

typedef struct _ManageableAgents {
    unsigned long count;
    unsigned long registered;

    ManagedAgent *list;
} ManageableAgents;

typedef struct _ManagedServer {
    DMCServerFlags flags;

    struct {
        struct sockaddr_in clear;
        struct sockaddr_in ssl;
    } address;

    time_t startTime;

    ManageableAgents agents;

    unsigned long rdnOffset;

    unsigned char identity[MDB_MAX_OBJECT_CHARS + 1];
} ManagedServer;

typedef struct _ManageableServers {
    unsigned long count;

    ManagedServer *list;
} ManageableServers;

typedef enum tagDMCFLAGS {
    DMC_FLAG_FAILED_SHUTDOWN = (1 << 0), 
    DMC_FLAG_MODULE_LOADED = (1 << 1), 
    DMC_FLAG_MODULE_ENABLED = (1 << 2), 
    DMC_FLAG_MODULE_DMC = (1 << 3), 
    DMC_FLAG_MODULE_NMAP = (1 << 4), 
    DMC_FLAG_MODULE_QUEUE = (1 << 5), 
    DMC_FLAG_MODULE_SMTP = (1 << 6), 
    DMC_FLAG_VERBOSE = (1 << 7), 
    DMC_FLAG_FALSIFY_LOAD = (1 << 8), 
    DMC_FLAG_MAX_FLAGS = (1 << 31)
} DMCFlags;

typedef struct tagDMCAgent {
    struct tagDMCAgent *next;
    struct tagDMCAgent *previous;

    unsigned char name[XPL_MAX_PATH + 1];
    DMCFlags flags;
    unsigned int priority;
    unsigned int load;
    void *data;
} DMCAgent;

typedef struct _DMCClient {
    DMCClientStates state;
    DMCClientFlags flags;

    Connection *user;
    Connection *resource;

    ManagedServer *server;
    ManagedAgent *agent;

    ProtocolCommand *command;

    unsigned long rdnOffset;

    unsigned char credential[128];
    unsigned char identity[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char buffer[CONN_BUFSIZE + 1];
} DMCClient;

struct {
    DMCStates state;

    BOOL stopped;

    XplRWLock lock;

    time_t startUpTime;

    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char rdn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char webAdmin[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char credential[NMAP_HASH_SIZE + 1];

    ProtocolCommandTree commands;
    ProtocolCommandTree bridged;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        unsigned int count;

        DMCAgent *head;
        DMCAgent *tail;
    } agents;

    struct {
        struct {
            BOOL enable;

            unsigned long options;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;

            unsigned short port;
        } ssl;

        Connection *conn;

        XplAtomic active;

        ManageableServers managed;

        struct sockaddr_in addr;

        unsigned short port;
    } server;

    struct {
        XplSemaphore semaphore;

        struct {
            BOOL enable;

            unsigned long options;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        struct {
            XplSemaphore todo;

            XplAtomic maximum;
            XplAtomic active;
            XplAtomic idle;

            Connection *head;
            Connection *tail;
        } worker;

        void *pool;

        time_t sleepTime;
    } client;

    struct {
        int count;

        unsigned long version;
        unsigned long *hosts;
    } trusted;

    struct {
        struct {
            BongoStatistics base;
            StatisticsStruct server;
            AntispamStatisticsStruct spam;
        } data;

        BongoStatistics *base;
        StatisticsStruct *server;
        AntispamStatisticsStruct *spam;
    } stats;

    struct {
        void *logging;
        MDBHandle directory;
    } handles;

    struct {
        BOOL enable;

        unsigned long version;
        unsigned long interval;
    } monitor;

    struct {
        int reload;
    } times;

} DMC;

int DMCMain(int argc, char *argv[]);
void DMCUnload(void);
BOOL DMCAgentPrep(DMCAgent *Agent);
BOOL DMCStartAgent(DMCAgent *Agent);
void DMCAgentRelease(DMCAgent *Agent);
void DMCSignalHandler(int sigtype);
void DMCKillAgents(void);

#endif    /*    DMCP_H    */

