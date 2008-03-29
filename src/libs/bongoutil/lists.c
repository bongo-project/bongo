/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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
// (C) 2008 Patrick Felt

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>

BongoSList * 
BongoSListCopy(BongoSList *slist)
{
    BongoSList *l;
    BongoSList *ret = NULL;
    BongoSList *last = NULL;

    for (l = slist; l != NULL; l = l->next) {
        BongoSList *newPiece;
        newPiece = MemNew0(BongoSList, 1);
        if (last) {
            last->next = newPiece;
        } else {
            ret = newPiece;
        }
        newPiece->data = l->data;
        last = newPiece;
    }

    return ret;
}

void
BongoSListFree(BongoSList *slist)
{
    BongoSList *next;
    
    if (!slist) {
        return;
    }

    do {
        next = slist->next;
        MemFree(slist);
        slist = next;
    } while (slist);
}

void
BongoSListFreeDeep(BongoSList *slist)
{
    BongoSList *next;
    
    if (!slist) {
        return;
    }

    do {
        next = slist->next;
        MemFree(slist->data);
        MemFree(slist);
        slist = next;
    } while (slist);
}

BongoSList *
BongoSListAppend(BongoSList *slist, void *data)
{
    BongoSList *last;
    BongoSList *piece;
    last = slist;
    while (last && last->next) {
        last = last->next;
    }
    
    piece = MemNew0(BongoSList, 1);
    piece->data = data;
    
    if (last) {
        last->next = piece;
        return slist;
    } else {
        return piece;
    }
}

BongoSList *
BongoSListAppendSList(BongoSList *slist, BongoSList *slist2)
{
    BongoSList *last;

    last = slist;
    while (last && last->next) {
        last = last->next;
    }
    
    if (last) {
        last->next = slist2;
        return slist;
    } else {
        return slist2;
    }
}


BongoSList *
BongoSListPrepend(BongoSList *slist, void *data)
{
    BongoSList *piece;
    
    piece = MemNew0(BongoSList, 1);
    piece->data = data;
    piece->next = slist;
    return piece;
}

BongoSList *
BongoSListReverse(BongoSList *slist)
{
    BongoSList *prev;

    prev = NULL;
    while (slist) {
        BongoSList *next;
        
        next = slist->next;
        slist->next = prev;
        prev = slist;
        slist = next;
    }

    return prev;
}

int
BongoSListLength(BongoSList *slist)
{
    int i = 0;
    while (slist) {
        i++;
        slist = slist->next;
    }
    return i;
}

BongoSList *
BongoSListDelete(BongoSList *list, void *data, BOOL deep)
{
    BongoSList *result=list;
    BongoSList *cur=list;
    BongoSList *prev=NULL;

    if (!list) {
        return result;
    }

    while (cur) {
        if (cur->data == data) {
            if (cur == list) {
                /* the item we are deleting is the first in the list.  i need
                * to reset the head of the list */
                result = list->next;
            }

            /* unlink this item */
            if (prev) {
                prev->next = cur->next;
            }

            if (deep) {
                MemFree(cur->data);
            }
            MemFree(cur);
            break;
        }
        prev=cur;
        cur=cur->next;
    }

    return result;
}

BongoList * 
BongoListCopy(BongoList *list)
{
    BongoList *l;
    BongoList *ret = NULL;
    BongoList *last = NULL;

    for (l = list; l != NULL; l = l->next) {
        BongoList *newPiece;
        newPiece = MemNew0(BongoList, 1);
        if (last) {
            last->next = newPiece;
            newPiece->prev = last;
        } else {
            ret = newPiece;
        }
        newPiece->data = l->data;
        last = newPiece;
    }

    return ret;
}

void
BongoListFree(BongoList *list)
{
    BongoList *next;
    
    if (!list) {
        return;
    }

    do {
        next = list->next;
        MemFree(list);
        list = next;
    } while (list);
}

void
BongoListFreeDeep(BongoList *list)
{
    BongoList *next;
    
    if (!list) {
        return;
    }

    do {
        next = list->next;
        MemFree(list->data);
        MemFree(list);
        list = next;
    } while (list);
}

BongoList *
BongoListAppend(BongoList *list, void *data)
{
    BongoList *last;
    BongoList *piece;

    last = list;
    while (last && last->next) {
        last = last->next;
    }
    
    piece = MemNew0(BongoList, 1);
    piece->data = data;
    
    if (last) {
        last->next = piece;
        piece->prev = last;
        return list;
    } else {
        return piece;
    }
}

BongoList *
BongoListAppendList(BongoList *list, BongoList *list2)
{
    BongoList *last;

    last = list;
    while (last && last->next) {
        last = last->next;
    }
    
    if (last) {
        last->next = list2;
        list2->prev = last;
        return list;
    } else {
        return list2;
    }
}

BongoList *
BongoListPrepend(BongoList *list, void *data)
{
    BongoList *piece;

    piece = MemNew0(BongoList, 1);
    piece->data = data;
    piece->next = list;
    if (list) {
        list->prev = piece;
    }
    
    return piece;    
}

BongoList *
BongoListReverse(BongoList *list)
{
    BongoList *last;

    last = NULL;
    while (list) {
        last = list;
        list = last->next;
        last->next = last->prev;
        last->prev = list;
    }

    return last;
}

int
BongoListLength(BongoList *list)
{ 
    int i = 0;
    while (list) {
        i++;
        list = list->next;
    }
    return i;
}

BongoList *
BongoListDelete(BongoList *list, void *data, BOOL deep)
{
    BongoList *result=list;
    BongoList *cur=list;

    if (!list) {
        return result;
    }

    while (cur) {
        if (cur->data == data) {
            if (cur == list) {
                /* the item we are deleting is the first in the list. i need
                 * to reset the head of the list */
                result = cur->next;
            }

            /* unlink this item */
            if (cur->next) {
                cur->next->prev = cur->prev;
            }

            if (cur->prev) {
                cur->prev->next = cur->next;
            }

            /* free the memory */
            if (deep) {
                MemFree(cur->data);
            }
            MemFree(cur);
            break;
        }
        cur = cur->next;
    }

    return result;
}
