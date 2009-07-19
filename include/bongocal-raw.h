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

#ifndef BONGOCAL_RAW_H
#define BONGOCAL_RAW_H

#include <xpl.h>
#include <libical/ical.h>
#include <bongojson.h>

XPL_BEGIN_C_LINKAGE

BongoJsonObject *BongoIcalPropertyToJson(icalproperty *prop);
BongoJsonObject *BongoIcalPropertyToJson(icalproperty *prop);

BongoJsonObject *BongoIcalComponentToJson(icalcomponent *comp, BOOL recurse);
icalcomponent *BongoJsonComponentToIcal(BongoJsonObject *object, icalcomponent *parent, BOOL recurse);

icalcomponent *BongoCalJsonToIcal(BongoJsonObject *obj);
BongoJsonObject *BongoCalIcalToJson(icalcomponent *comp);

XPL_END_C_LINKAGE

#endif
