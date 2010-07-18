#ifndef CONTACTS_H
#define CONTACTS_H
    
#include "stored.h"

const char * StoreProcessIncomingContact(StoreClient *client, StoreObject *contact, const char *path);

#endif
