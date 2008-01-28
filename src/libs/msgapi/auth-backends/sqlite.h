
int	AuthSqlite_GetDbPath(char *path, size_t size);
int	AuthSqlite_GenerateHash(const char *username, const char *password, char *result, size_t result_len);
int	AuthSqlite_Install(void);
int	AuthSqlite_FindUser(const char *user);
int	AuthSqlite_VerifyPassword(const char *user, const char *password);
int	AuthSqlite_AddUser(const char *user);
int	AuthSqlite_SetPassword(const char *user, const char *password);
int	AuthSqlite_GetUserStore(const char *user, struct sockaddr_in *store);
int	AuthSqlite_UserList(char **list[]);
int	AuthSqlite_InterfaceVersion(void);
int AuthODBC_Init(void);
