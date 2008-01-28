// Plugin API definition

typedef struct {
	BOOL available;
	BOOL optional;
	const char *name;
	void *func;
} MsgAuthAPIFunction;

typedef int (* AuthPlugin_InterfaceVersion)(void);
typedef int (* AuthPlugin_FindUser)(const char *user);
typedef int (* AuthPlugin_VerifyPassword)(const char *user, const char *password);
typedef int (* AuthPlugin_AddUser)(const char *user);
typedef int (* AuthPlugin_ChangePassword)(const char *user, const char *oldpassword, const char *newpassword);
typedef int (* AuthPlugin_SetPassword)(const char *user, const char *password);
typedef int (* AuthPlugin_UserList)(char **list[]);
typedef int (* AuthPlugin_GetUserStore)(const char *user, struct sockaddr_in *store);
typedef int (* AuthPlugin_Install)(void);
typedef int (* AuthPlugin_Init)(void);

enum {
	Func_InterfaceVersion = 0,
	Func_FindUser = 1,
	Func_UserList = 2,
	Func_VerifyPassword = 3,
	Func_AddUser = 4,
	Func_ChangePassword = 5,
	Func_SetPassword = 6,
	Func_GetUserStore = 7,
	Func_Install = 8,
	Func_Init = 9
};

static MsgAuthAPIFunction pluginapi[] = {
	{0, 0, "InterfaceVersion", NULL},
	{0, 0, "FindUser", NULL},
	{0, 0, "UserList", NULL},
	{0, 0, "VerifyPassword", NULL},
	{0, 1, "AddUser", NULL},
	{0, 1, "ChangePassword", NULL},
	{0, 0, "SetPassword", NULL},
	{0, 1, "GetUserStore", NULL},
	{0, 0, "Install", NULL},
    {0, 1, "Init", NULL},
	{0, 0, NULL, NULL}
};
