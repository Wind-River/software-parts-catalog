## Software Parts Data Model

Maintaining a catalog of software components (parts) is a requirement whether generating SBOMs for managing license compliance, security assurance, export control, or safety certification. We developed a highly searchable scalable software parts database (catalog) enabling companies to seamlessly manage 1000s, 10,000s if not 100,000s of software parts from which their products are comprised. The catalog stores specific intrinsic data for each software part. For example, id, name, version, content (binary or source code), size, the licensing, legal notices, CVE information and so forth. 

A software part is any software component that represents a set of one or more software files from which larger software solutions may be comprised. Our definition supports a wide range of software types (parts). A part can be:
  - single source file (e.g., main.c)
  - single runtime binary file (e.g., app.exe)
  - a container image file 
  - a collection of parts (files)  - i.e., an archive of two  or more smaller software parts (e.g., busybox.1.32.1.tar.gz, file.rpm). It may include source, binary files, linbrary files, other archives and/or container images. 
  - a container's content - (e.g., a collection of archives, source files and binaries)

The catalog stores specific intrinsic data for each software part. For example, the license

## Part Types
| Type              | Comprised Of* | Examples | Notes |
|-------------------| ------------ | -------- | ----- |
| /part/file/src      | n/a | main.c, main.js      | Uploaded as an archive of 1 file |
| /part/file/binary/app       | link | app.exe     | Uploaded as an archive of 1 file |
| /part/file/binary/library     | link | libdb.so     | Uploaded as an archive of 1 file |
| /part/file/collection | link | busybox.1.31.2.tar.gz |  |
| /part/file/binary/container   | link | |  |
| /part/collection/contents     | logical | vxworks7-22.09, wr-studio-22.06 | Complex composite product |
| /part/file/binary/runtime     | link | linux runtime binary | Uploaded as an archive of 1 file |

*The 'comprised of' column notes whether the type may have a link to a list of sub parts or logical structure (e.g., logical tree structure). 

### List of Part Data fields
- UUID
- Type
- Name (e.g., busybox -> busybox-1.32.5.tar.gz)
- Version (optional) (e.g., 1.32.5)
- Family Name (e.g., /busybox/1.x)
- Content
  - List of archives
- FVC (if applicable)
- Size (kb)
- License
- License rational
- Automation License
- Legal Notices
- List of CVEs
- List of aliases*
- List of archives 
- Cryptography algorithm*
- List of alternative external links/references*


## Catalog & Part IDs (Identification) 
Software parts have a  unique id which identifies both the catalog instance where it is stored along with a pointer (id) to the part record within the catalog. Therefor each instance of the catalog is given a unique identifier with the following format:
  ```
    [domain-name]/[catalog-instance-id]
  ```
Where:
- **[domain-name]** is a unique human readable alphanumeric string that uniquely identifies the organization that created/maintains one or more catalog instances. A registry will eventually be maintained where organizations can register their organization identifiers. 
- **[catalog-instance-id]** is a unique alphanumeric string that uniquely identifies an instance of a catalog maintained by the organization. 

For example, The Telsa Motors (Telsa) might maintain the following internal instance identified as:
  tesla.com/dev-catalog
  
Or Tesla may choose to hosted two or more different instances of a catalog and therefore it would assign different names to each instance - e.g.,
  - tesla.com/dev-catalog - internal engineering instance
  - tesla.com/public-1 - publically available instance available to its suppliers
 
Individual parts stored in the catalog have the following format:

```
  partid://[domain-name]/[catalog-instance-id]/UUID
```
Where the **UUID** is the database primary key of the part. For example, if the part in a catalog had the following UUID: **2503451233** the part would have the following externally referenceable id:
```
partid://tesla.com/public-1/2503451233
```
If a file verification code is available for a part, then the part could also be identified by the following id:
```
fvc://[domain-name]/[catalog-instance-id]/[fvc]
```
Where **[fvc]** represents the file verification code of a part (if it has one). For example, if our current instance of the parts catalog is referenced as: **tesla.com/public-1** with a part having the following fvc:
```
4656433200e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```
then the following id would also unique identify the part:
```
fvcid://tesla.com/public-1/4656433200e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
```

### Part Alises
One may want to provide a human readbale identification to a part. One could specific an alpha-numberic string that can include '-' and '_'. That is characters you would typically use for a programe variable. It would take on the following format:
  ```
  alaisid://[domain-name]/[catalog-instance-id]/[alias]
  ```
Where **[alias]** is a unique human readable alphanumeric string that uniquley identifies the part with inthe context of a specific catalog instance. 

For example for a product name VxWorks7 version 22.09 maintained by Wind R you might define the following alias 
- **vxworks-22.09** -> partalias://windriver.com/spc/vxworks-22.09

## Licensing & License Expressions
Licensing information is an intrinsic attribute of each software part and therefore the Software Parts Catalog (SPC) needs a way to represent and store this information. Although there are some standards around how to name a collection of commonly used licenses (e.g., SPDX license list) there is no standard on how to represent the universe of all possible licenses an organization can find in the wild. Although the SPDX license provides a solid foundation for standardizing on the most commonly used licenses, it is not sufficient to represent the myriad of licenses an organization will encounter. The SPC mission is to deliver a catalog platform to represent all the software parts used by an organization which must include a mechanism to represent all licenses they will encounter include third-party commercial licenses. For example, as of September 2022 the SPDX license list has 400+ licenses yet Wind River has identified over 1800 unique licenses (and growing). 

Different organizations can choose different ways to represent license expressions (including the SPDX framework) and therefore the SPC provides the ability to support a myriad of different approaches. To achieve that the SPC stores license information as a string which allows one to store license expressions using a syntax and semantics of their choice. The system comes with a default license expression platform that represents over 1800 licenses found in the wild (including all the ones included in the SPDX license list). If you choose replace the default mechanism you will need to implement functions to provide the following:
  * Validate the string is syntactically correct
  * Handle licenses identified yet do not have a assigned a unique license id
  * Converts license expression strings into a human readable expression for human consumption.
  * Convert license expression strings into SPDX identifiers
  * Display the various record fields where different licenses may have slightly different fields. 

For example, although the GPL version 2.0 license is represented using multiple different identifiers such as GPLv2, GPL-2.0, GPL-2.0-only, the default license management system assigns a single unique internal id (e.g., _gpl_2.0). A dual GPL-2.0 and MIT license expression would be represented internally using the following string: “_gpl-2.0 AND _mit”. The validation function would check that the syntax is correct before assigning it to the license field and the function that converts it into human readable form would output: GPL-2.0 OR MIT. The system would generate the following expression if it was to be represented in an SPDX document: GPL-2.0-only OR MIT. 

To support the ability to have different licensing record fields for different licenses, the data is represented in the SPC as json objects (records) to provide the required flexibility. For example, one license may have several notes or external url references where another may have none.   
When a new license is identified the default licensing system represents it as “CUSTOM[<identifier>]” Where CUSTOM is a key word and <identifier>). One is expected to provide the corresponding license text for CUSTOM designated licenses.  

