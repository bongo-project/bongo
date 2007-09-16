#include <stdio.h>
#include <config.h>
#include <xpl.h>
#include <msgapi.h>

/* returns 0 on success */
int
AuthLdap_Install(void)
{
	// TODO
	return -1;
}

int
AuthLdap_FindUser(const char *user)
{
	return -1;
}

int
AuthLdap_VerifyPassword(const char *user, const char *password)
{
	return -1;
}

/* "Write" functions below */

int
AuthLdap_AddUser(const char *user)
{
	return -1;
}

int
AuthLdap_SetPassword(const char *user, const char *password)
{
	return -1;
}

int
AuthLdap_InterfaceVersion(void)
{
	return 1;
}

int
main (int argc, char *argv[])
{
	printf("This cannot be run directly");
	return (-1);
}
