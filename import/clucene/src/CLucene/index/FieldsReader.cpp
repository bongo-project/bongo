/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "FieldsReader.h"

#include "CLucene/util/VoidMap.h"
#include "CLucene/util/Misc.h"
#include "CLucene/store/Directory.h"
#include "CLucene/document/Document.h"
#include "CLucene/document/Field.h"
#include "FieldInfos.h"

CL_NS_USE(store)
CL_NS_USE(document)
CL_NS_USE(util)
CL_NS_DEF(index)

	FieldsReader::FieldsReader(Directory* d, const char* segment, FieldInfos* fn):
		fieldInfos(fn)
	{
    //Func - Constructor
	//Pre  - d contains a valid reference to a Directory
	//       segment != NULL
    //       fn contains a valid reference to a FieldInfos
	//Post - The instance has been created

		CND_PRECONDITION(segment != NULL, "segment != NULL");

		const char* buf = Misc::segmentname(segment,".fdt");
		fieldsStream = d->openInput( buf );
		_CLDELETE_CaARRAY( buf );

		buf = Misc::segmentname(segment,".fdx");
		indexStream = d->openInput( buf );
		_CLDELETE_CaARRAY( buf );

		_size = (int32_t)indexStream->length()/8;
	}

    FieldsReader::~FieldsReader(){
    //Func - Destructor
	//Pre  - true
	//Post - The instance has been destroyed

		close();
    }
    
	void FieldsReader::close() {
    //Func - Closes the FieldsReader
    //Pre  - true
	//Post - The FieldsReader has been closed
        
		if (fieldsStream){
		    fieldsStream->close();
		    }
		if(indexStream){
		    indexStream->close();
		    }
		_CLDELETE(fieldsStream);
		_CLDELETE(indexStream);
	}

	int32_t FieldsReader::size() {
		return _size;
	}

	Document* FieldsReader::doc(int32_t n) {
		indexStream->seek(n * 8L);
		int64_t position = indexStream->readLong();
		fieldsStream->seek(position);
	    
		Document* doc = _CLNEW Document();
		int32_t numFields = fieldsStream->readVInt();
		for (int32_t i = 0; i < numFields; i++) {
			int32_t fieldNumber = fieldsStream->readVInt();
			FieldInfo* fi = fieldInfos->fieldInfo(fieldNumber);

			uint8_t bits = fieldsStream->readByte();
			TCHAR* fvalue = fieldsStream->readString(true);
			Field* f = _CLNEW Field(
							fi->name,		  // name
							fvalue, // read value
							true,			  // stored
							fi->isIndexed,		  // indexed
							(bits & 1) != 0, fi->storeTermVector); //vector
			_CLDELETE_CARRAY(fvalue);

			doc->add( *f );	  // tokenized
		}

		return doc;
	}

CL_NS_END
