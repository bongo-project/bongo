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

#if 0
#include <config.h>
#include <xplutil.h>
#include <xplold.h>
#include <errno.h>

#if defined(NETWARE)
XPLNETDB_DEFINE_CONTEXT
#endif

#if defined(SOLARIS) || defined(LINUX) || defined(S390RH)
#include <sys/utsname.h>
#include <netdb.h>
#include <arpa/inet.h>
#include <sys/poll.h>

unsigned long
XplGetHostIPAddress(void)
{
	unsigned long	Address;
	struct utsname	name;
	struct hostent	*he;

	uname(&name);

	he = gethostbyname(name.nodename);
	if (he) {
		memcpy(&Address, he->h_addr_list[0], sizeof(Address));
		return(Address);
	}

	return(inet_addr("127.0.0.1"));
}


#if 0 /* JNT */
BOOL 
XplIsLocalIPAddress(unsigned long Address)
{
	int				i;
	struct utsname	name;
	struct hostent	*he;

	uname(&name);

	he = gethostbyname(name.nodename);
	if (he) {
		for (i = 0; he->h_addr_list[i] != NULL; i++) {
			if (Address == *((unsigned long *)(he->h_addr_list[i]))) {
				return(TRUE);
			}
		}
	}

	return((Address == inet_addr("127.0.0.1")));
}
#else
#ifndef __unp_ifi_h
#define __unp_ifi_h

/* Get interface list - adapted from Richard Steven's book */
#include <sys/ioctl.h>
#include <net/if.h>

struct ifi_info {
  short ifi_flags; /* IFF_xxx constants from <net/if.h> */
  short ifi_myflags; /* our own IFI_xxx flags */
  struct sockaddr *ifi_addr; /* primary address */
  struct sockaddr *ifi_brdaddr; /* broadcast address */
  struct sockaddr *ifi_dstaddr; /* destination address */
  struct ifi_info *ifi_next; /* next of these structures */
};

#define IFI_ALIAS 1/* ifi_addr is an alias */

/* function prototypes */
struct ifi_info *get_ifi_info(int, int);
void free_ifi_info(struct ifi_info *);

#endif /* __unp_ifi_h */

struct ifi_info *
get_ifi_info(int family, int doaliases)
{
  struct ifi_info *ifi, *ifihead, **ifipnext;
  int sockfd, len, lastlen, flags, myflags;
  char *ptr,  *buf = NULL, lastname[IFNAMSIZ], *cptr;
  struct ifconf ifc;
  struct ifreq *ifr, ifrcopy;
  struct sockaddr_in *sinptr;
  
  sockfd = socket(AF_INET, SOCK_DGRAM, 0);
  
  lastlen = 0;
  len = 100 * sizeof(struct ifreq); /* initial buffer size guess */
  for ( ; ; ) {
    buf = malloc(len);
    ifc.ifc_len = len;
    ifc.ifc_buf = buf;
    if (ioctl(sockfd, SIOCGIFCONF, &ifc) < 0) {
      if (errno != EINVAL || lastlen != 0)
	perror("getifi_info ioctl error");
    } else {
      if (ifc.ifc_len == lastlen)
	break; /* success, len has not changed */
      lastlen = ifc.ifc_len;
    }
    len += 10 * sizeof(struct ifreq); /* increment */
    free(buf);
  }
  ifihead = NULL;
  ifipnext = &ifihead;
  lastname[0] = 0;

  for (ptr = buf; ptr < buf + ifc.ifc_len;) {
    ifr = (struct ifreq *) ptr;

#ifdef HAVE_SOCKADDR_SA_LEN
    len = max(sizeof(struct sockaddr), ifr->ifr_addr.sa_len);
#else
    switch(ifr->ifr_addr.sa_family) {
#ifdef IPV6
    case AF_INET6:
      len = sizeof(struct sockaddr_in6);
      break;
#endif
    case AF_INET:
    default:
      len = sizeof(struct sockaddr);
      break;
    }

#endif /* HAVE_SOCKADDR_SA_LEN */

    ptr += sizeof(ifr->ifr_name) + len; /* for next one in buffer */
    
    if (ifr->ifr_addr.sa_family != family)
      continue; /* ignore if not desired address family */
    
    myflags = 0;
    if ((cptr = strchr (ifr->ifr_name, ':')) != NULL) {
      *cptr = 0; /* replace colon with NULL */
    }
    if (strncmp(lastname, ifr->ifr_name, IFNAMSIZ ) == 0) {
      if (doaliases == 0) {
	continue; /* already processed this interface */
      }

      myflags = IFI_ALIAS;
    }
    memcpy(lastname, ifr->ifr_name, IFNAMSIZ);
    ifrcopy = *ifr;
    ioctl(sockfd, SIOCGIFFLAGS, &ifrcopy);
    flags = ifrcopy.ifr_flags;
    if ((flags & IFF_UP) == 0) {
      continue; /* ignore if interface not up */
    }
    ifi = malloc(sizeof(struct ifi_info));
    memset(ifi, 0, sizeof(struct ifi_info));
    *ifipnext = ifi; /* prev points to this new one */
    ifipnext = &ifi->ifi_next; /* pointer to next one goes here */
    ifi->ifi_flags = flags; /* IFF_xxx values */
    ifi->ifi_myflags = myflags; /* IFI_NAME */

    switch (ifr->ifr_addr.sa_family) {
    case AF_INET:
      sinptr = (struct sockaddr_in *)&ifr->ifr_addr;
      if(ifi->ifi_addr == NULL) {
	ifi->ifi_addr = calloc(1, sizeof(struct sockaddr_in));
	memcpy(ifi->ifi_addr, sinptr, sizeof(struct sockaddr_in));
#ifdef SIOCGIFBRDADDR
	if (flags & IFF_BROADCAST) {
	  ioctl(sockfd, SIOCGIFBRDADDR, &ifrcopy);
	  ifi->ifi_brdaddr = malloc(sizeof(struct sockaddr_in));
	  memset(ifi->ifi_brdaddr, 0, sizeof(struct sockaddr_in));
	  memcpy(ifi->ifi_brdaddr, sinptr, sizeof(struct sockaddr_in));
	  memcpy(ifi->ifi_brdaddr, sinptr, sizeof(struct sockaddr_in));
	}
#endif
#ifdef SIOCGIFDSTADDR
	if(flags & IFF_POINTOPOINT) {
	  ioctl(sockfd, SIOCGIFDSTADDR, &ifrcopy);
	  sinptr = (struct sockaddr_in *)&ifrcopy.ifr_dstaddr;
	  ifi->ifi_dstaddr = calloc(1, sizeof(struct sockaddr_in));
	  memcpy(ifi->ifi_dstaddr, sinptr, sizeof(struct sockaddr_in));
	}
#endif
      }
      break;
    default:
      break;
    }
  }
  free (buf);
  return(ifihead);    
}


void free_ifi_info(struct ifi_info *ifihead)
{
  struct ifi_info *ifi, *ifinext;
  for (ifi = ifihead; ifi != NULL; ifi = ifinext) {
    if (ifi->ifi_addr != NULL)
      free(ifi->ifi_addr);
    if (ifi->ifi_brdaddr != NULL)
      free(ifi->ifi_brdaddr);
    if (ifi->ifi_dstaddr != NULL)
      free(ifi->ifi_dstaddr);
    ifinext = ifi->ifi_next; /* can't fetch ifi_next after free() */
    free(ifi); /* the ifi_info {} itself */
  }
}

struct ifi_info *InterfaceList = NULL;

char *Sock_ntop_host(struct sockaddr *sa, int len) 
{
  if (sa->sa_family == AF_INET) {
    struct sockaddr_in *sin = (struct sockaddr_in *)sa;

    return(inet_ntoa(sin->sin_addr));
  }
  return("NULL");

}

int XplGetInterfaceList(void) 
{
  struct sockaddr *sa;

	if (InterfaceList != NULL) {
		free_ifi_info(InterfaceList);
	}

	InterfaceList = get_ifi_info(AF_INET, 1);

	/* do we want to log this information ??? */
#if 0
  {
    struct ifi_info *ifi;
    for (ifi = InterfaceList; ifi != NULL; ifi = ifi->ifi_next) {
      
      if (ifi->ifi_flags & IFF_UP) printf("UP ");
      if (ifi->ifi_flags & IFF_BROADCAST) printf("BCAST ");
      if (ifi->ifi_flags & IFF_MULTICAST) printf("MCAST ");
      if (ifi->ifi_flags & IFF_LOOPBACK) printf("LOOP ");
      if (ifi->ifi_flags & IFF_POINTOPOINT) printf("P2P ");
      printf(">\n");
      
      if ((sa = ifi->ifi_addr) != NULL) 
	printf("  IP addr: %s\n",
	       Sock_ntop_host(sa, sizeof(*sa)));
      if ( (sa = ifi->ifi_brdaddr) != NULL)
	printf("  broadcast addr: %s\n", 
	       Sock_ntop_host(sa, sizeof(*sa)));
      if ((sa = ifi -> ifi_dstaddr) != NULL) 
	printf(" destination addr: %s\n", 
	       Sock_ntop_host(sa, sizeof(*sa)));
    }
  }
#endif

  return(0);
}

int XplDestroyInterfaceList()
{
  if (InterfaceList) {
    free_ifi_info(InterfaceList);
    InterfaceList = NULL;
  }

  return(0);
}

BOOL
XplIsLocalIPAddress(unsigned long Address)
{
	struct ifi_info *ifi;
	struct sockaddr_in *sin;

	for (ifi = InterfaceList; ifi != NULL; ifi=ifi->ifi_next) {
		/* sanity check address restrict to IPv 4 */
		if ((ifi->ifi_addr != NULL) && (ifi->ifi_addr->sa_family == AF_INET)) {
			sin = (struct sockaddr_in *)ifi->ifi_addr;
			if (sin->sin_addr.s_addr == Address) {
				return(TRUE);
			}
		}
	}

	return(FALSE);
}

#endif

int
XplIPRead(void *socket, unsigned char *Buf, int Len, int readTimeout)
{
  int					rc;
  struct pollfd	pfd;

XPLIPReadPoll_retry:

   /* All sockets are closed when exiting.  */
   /* Closed socket will error on read. */
   pfd.fd = (int)socket;
   pfd.events = POLLIN;
   rc = poll(&pfd, 1, readTimeout * 1000);
	if (rc > 0) {
		/*success */
		/* see if an error ocurred on this socket */
		if (!(pfd.revents & (POLLERR | POLLHUP | POLLNVAL))) {

XPLIPRead_Again:

			rc = recv((int)socket, Buf, Len, 0);
			if (rc >= 0) {
				/* success  */
				return(rc);
			} else if (errno == EINTR) {
				goto XPLIPRead_Again;
			}

			/* read error(-1) or end of connection (0) */
			return(rc);
		}

		/* pfd.revents error received */
		return(-1);
	} else if (rc == 0) {
		/* poll timeout */
		return(-1);
	}

	return(-1);
}


int
XplIPWrite(void * socket, unsigned char *Buf, int Len)
{
	int rc;

XPLIPWrite_Again:

	rc = send((int)socket, Buf, Len, 0);
	if (rc >= 0) {
		/* common predicted path */
		return(rc);
	} else if (errno == EINTR) {
		goto XPLIPWrite_Again;
	}

	return(-1);
}
#endif /* SOLARIS || LINUX || S390RH */

#if defined(WIN32)
unsigned long
XplGetHostIPAddress(void)
{
	unsigned long	Address;
	unsigned char	Name[256];
	struct hostent	*he;

	gethostname(Name, sizeof(Name));

	he = gethostbyname(Name);
	if (he) {
		memcpy(&Address, he->h_addr_list[0], sizeof(Address));
		return(Address);
	}

	return(inet_addr("127.0.0.1"));
}

BOOL 
XplIsLocalIPAddress(unsigned long Address)
{
	int				i;
	unsigned char	name[256];
	struct hostent	*he;

	gethostname(name, sizeof(name));
	he = gethostbyname(name);
	if (he) {
		for (i = 0; he->h_addr_list[i] != NULL; i++) {
			if (Address == *((unsigned long *)(he->h_addr_list[i]))) {
				return(TRUE);
			}
		}
	}

	return((Address == inet_addr("127.0.0.1")));
}

int
XplIPRead(void * socket, unsigned char *Buf, int Len, int readTimeout)
{
	int				rc;
	fd_set			readfds;
	struct timeval	timeout;

	FD_ZERO(&readfds);
	FD_SET( (int)socket,&readfds);
	timeout.tv_usec = 0;
	timeout.tv_sec = readTimeout;

	/* All sockets are closed when exiting.  */
	/* Closed sockets will error on read. */
	rc = select(FD_SETSIZE, &readfds, NULL,NULL,&timeout);
	if (rc > 0) {
		/*success - no special handling of read */
		return(recv((int)socket, Buf, Len, 0));
	}

	/* return -1 on timeout or select error  */
	return(-1);
}

int XplIPWrite(void * socket, unsigned char *Buf, int Len)
{
	return(send((int)socket, Buf, Len,0));
}
#endif	/*	defined(WIN32)	*/

#if defined(NETWARE)
BOOL 
XplIsLocalIPAddress(unsigned long Address)
{
	if ((Address == gethostid()) || (Address == inet_addr("127.0.0.1"))) {
		return(TRUE);
	}

	return(FALSE);
}

int
XplIPRead(void *socket, unsigned char *Buf, int Len, int readTimeout)
{
	int rc;
	fd_set readfds;
	struct timeval timeout;

	FD_ZERO(&readfds);
	FD_SET( (int)socket,&readfds);
	timeout.tv_usec = 0;
	timeout.tv_sec = readTimeout;

	/* All sockets are closed when exiting.  */
	/* Closed sockets will error on read. */
	rc = select(FD_SETSIZE, &readfds, NULL,NULL,&timeout);
	if (rc > 0) {
		/*success - no special handling of read */
		return(recv((int)socket, Buf, Len, 0));
	}

	/* return -1 on timeout or select error  */
	return(-1);
}

int XplIPWrite(void *socket, unsigned char *Buf, int Len)
{
	return(send((int)socket, Buf, Len,0));
}
#endif	/*	defined(NETWARE)	*/

#if defined(LIBC)
unsigned long XplGetHostIPAddress(void)
{
#if 0
	unsigned long	Address;
	struct utsname	name;
	struct hostent	*he;

	/*	On NetWare LibC the uname function returns "NLM" as the nodename; while this
		will be fixed in the future it doesn't help us out right now.	*/
	uname(&name);

	he = gethostbyname(name.nodename);
	if (he) {
		memcpy(&Address, he->h_addr_list[0], sizeof(Address));
		return(Address);
	}
#else
	struct net_info	ni;

	memset(&ni, 0, sizeof(struct net_info));

	if ((netware_net_info(&ni) == 0) && (ni.IPAddrsBound[0])) {
		return(ni.IPAddrsBound[0]);
	}

#endif

	return(inet_addr("127.0.0.1"));
}

#error	XplIsLocalIPAddress not implemented
BOOL 
XplIsLocalIPAddress(unsigned long Address)
{
	return(FALSE);
}

int XplIPRead(void *socket, unsigned char *Buf, int Len, int readTimeout)
{
	int rc;
	fd_set readfds;
	struct timeval timeout;

	FD_ZERO(&readfds);
	FD_SET( (int)socket,&readfds);
	timeout.tv_usec = 0;
	timeout.tv_sec = readTimeout;

	/* All sockets are closed when exiting.  */
	/* Closed sockets will error on read. */
	rc = select(FD_SETSIZE, &readfds, NULL,NULL,&timeout);
	if (rc > 0) {
		/*success - no special handling of read */
		return(recv((int)socket, Buf, Len, 0));
	}

	/* return -1 on timeout or select error  */
	return(-1);
}

int XplIPWrite(void *socket, unsigned char *Buf, int Len)
{
	return(send((int)socket, Buf, Len,0));
}
#endif	/*	defined(LIBC)	*/
#endif
