/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "FieldsWriter.h"

#include "CLucene/util/VoidMap.h"
#include "CLucene/util/Reader.h"
#include "CLucene/util/Misc.h"
#include "CLucene/store/Directory.h"
#include "CLucene/store/IndexOutput.h"
#include "CLucene/document/Document.h"
#include "CLucene/document/Field.h"
#include "FieldInfos.h"

CL_NS_USE(store)
CL_NS_USE(util)
CL_NS_USE(document)
CL_NS_DEF(index)
	
	FieldsWriter::FieldsWriter(Directory* d, const char* segment, FieldInfos* fn):
		fieldInfos(fn)
	{
	//Func - Constructor
	//Pre  - d contains a valid reference to a directory
	//       segment != NULL and contains the name of the segment
	//Post - fn contains a valid reference toa a FieldInfos

		CND_PRECONDITION(segment != NULL,"segment is NULL");

		const char* buf = Misc::segmentname(segment,".fdt");
        fieldsStream = d->createOutput ( buf );
        _CLDELETE_CaARRAY( buf );
        
		buf = Misc::segmentname(segment,".fdx");
        indexStream = d->createOutput( buf );
        _CLDELETE_CaARRAY( buf );
          
		CND_CONDITION(indexStream != NULL,"indexStream is NULL");
    }

	FieldsWriter::~FieldsWriter(){
	//Func - Destructor
	//Pre  - true
	//Post - Instance has been destroyed

		close();
	}

	void FieldsWriter::close() {
	//Func - Closes all streams and frees all resources
	//Pre  - true
	//Post - All streams have been closed all resources have been freed

		//Check if fieldsStream is valid
		if (fieldsStream){
			//Close fieldsStream
			fieldsStream->close();
			_CLDELETE( fieldsStream );
			}

		//Check if indexStream is valid
		if (indexStream){
			//Close indexStream
			indexStream->close();
			_CLDELETE( indexStream );
			}
	}

	void FieldsWriter::addDocument(Document* doc) {
	//Func - Adds a document
	//Pre  - doc contains a valid reference to a Document
	//       indexStream != NULL
	//       fieldsStream != NULL
	//Post - The document doc has been added

		CND_PRECONDITION(indexStream != NULL,"indexStream is NULL");
		CND_PRECONDITION(fieldsStream != NULL,"fieldsStream is NULL");

		indexStream->writeLong(fieldsStream->getFilePointer());

		int32_t storedCount = 0;
		DocumentFieldEnumeration* fields = doc->fields();
		while (fields->hasMoreElements()) {
			Field* field = fields->nextElement();
			if (field->isStored())
				storedCount++;
		}
		_CLDELETE(fields);
		fieldsStream->writeVInt(storedCount);

		fields = doc->fields();
		while (fields->hasMoreElements()) {
			Field* field = fields->nextElement();
			if (field->isStored()) {
				fieldsStream->writeVInt(fieldInfos->fieldNumber(field->name()));

				uint8_t bits = 0;
				if (field->isTokenized())
					bits |= 1;
				//FEATURE: can this data be compressed? A bit could be set to compress or not compress???
				fieldsStream->writeByte(bits);

				//FEATURE: this problem in Java Lucene too, if using Reader, data is not stored.
				//todo: this is a logic bug...
				//if the field is stored, and indexed, and is using a reader the field wont get indexed
				if ( field->stringValue() == NULL ){
					CND_PRECONDITION(!field->isIndexed(), "Cannot store reader if it is indexed too")
					Reader* r = field->readerValue();
					
					//read the entire string
					const TCHAR* rv;
					int64_t rl = r->read(rv, LUCENE_INT32_MAX_SHOULDBE);
					if ( rl > LUCENE_INT32_MAX_SHOULDBE )
						_CLTHROWA(CL_ERR_Runtime,"Field length too long");

					fieldsStream->writeString( rv, (int32_t)rl);
				}else
					fieldsStream->writeString(field->stringValue(),_tcslen(field->stringValue()));
			}
		}
		_CLDELETE(fields);
	}

CL_NS_END
