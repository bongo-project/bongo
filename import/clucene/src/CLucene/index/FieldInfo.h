/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_FieldInfo_
#define _lucene_index_FieldInfo_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/util/StringIntern.h"

CL_NS_DEF(index)
    class FieldInfo :LUCENE_BASE{
	  public:
		//name of the field
		const TCHAR*     name;

        //Is field indexed? true = yes false = no
		bool        isIndexed;

		// true if term vector for this field should be stored
		bool storeTermVector;

		//What does number represent?
		const int32_t number;

      //todo: its probably not quicker to intern the fieldname,
      //but it could save memory. do a benchmark with the two methods
		FieldInfo(const TCHAR* na, const bool tk, const int32_t nu, const bool stv):
			name(CL_NS(util)::CLStringIntern::intern(na CL_FILELINE)),
			isIndexed(tk),
			storeTermVector(stv),
			number(nu){
        //Func - Constructor
		//       Initialises FieldInfo.
		//       na holds the name of the field
		//       tk indicates whether this field is indexed or not
		//       nu indicates its number
		//Pre  - na != NULL and holds the name of the field
		//       tk is true or false
		//       number >= 0
		//Post - The FieldInfo instance has been created and initialized.
		//       name holds the duplicated string of na
		//       isIndexed = tk
		//       number = nu  
	    }

	    ~FieldInfo(){
		//Func - Destructor
		//Pre  - true
		//Post - The instance has been destroyed
			CL_NS(util)::CLStringIntern::unintern(name);
	    }


	};
CL_NS_END
#endif
