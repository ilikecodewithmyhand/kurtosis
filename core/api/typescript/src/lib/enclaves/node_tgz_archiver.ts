/*
 * Copyright (c) 2022 - present Kurtosis Technologies Inc.
 * All Rights Reserved.
 */

import "neverthrow"
import {GenericTgzArchiver} from "./generic_tgz_archiver";
import {ok, err, Result} from "neverthrow";
import * as tar from "tar"
import * as filesystem from "fs"
import * as path from "path"
import * as os from "os";

const COMPRESSION_EXTENSION = ".tgz"
const GRPC_DATA_TRANSFER_LIMIT = 3999000 //3.999 Mb. 1kb wiggle room. 1kb being about the size of a 2 paragraph readme.
const COMPRESSION_TEMP_FOLDER_PREFIX = "temp-node-archiver-compression-"
export class NodeTgzArchiver implements GenericTgzArchiver{

     public async createTgzByteArray(pathToArchive: string): Promise<Result<Uint8Array, Error>> {
         //Check if it exists
         if (!filesystem.existsSync(pathToArchive)) {
             return err(new Error("The file or folder you want to upload does not exist."))
         }
         if (pathToArchive === "/") {
             return err(new Error("Cannot archive the root directory"))
         }

         //Make directory for usage.
         const osTempDirpath = os.tmpdir()
         const tempDirpathPrefix = path.join(osTempDirpath, COMPRESSION_TEMP_FOLDER_PREFIX)
         const tempDirpathResult = await filesystem.promises.mkdtemp(
             tempDirpathPrefix,
         ).then((folder: string) => {
             return ok(folder)
         }).catch((tempDirErr: Error) => {
             return err(tempDirErr)
         });
         if (tempDirpathResult.isErr()) {
             return err(new Error("Failed to create temporary directory for file compression."))
         }
         const tempDirpath = tempDirpathResult.value
         const destFilename = path.basename(pathToArchive) + COMPRESSION_EXTENSION
         const destFilepath = path.join(tempDirpath, destFilename)
         /*
         const archiveOptions = {
             src: pathToArchive,
             dest: path.join(tempPathResponse.value, baseName),
         }
          */
         const archiveOptions = {
             cwd: path.dirname(pathToArchive),
             file: destFilepath,
             gzip: true,
             async: true,
         }

         const targzPromise: Promise<Result<null, Error>> = tar.create(
             archiveOptions,
             [destFilename],
         ).then(() => {
             return ok(null)
         }).catch((err: any) => {
             if (err && err.stack && err.message) {
                 return err(err as Error);
             }
             return err(new Error(`A non-Error object '${err}' was thrown when compressing '${pathToArchive}' to '${destFilepath}'`))
         })
         const targzResult = await targzPromise
         if(targzResult.isErr()) {
             return err(targzResult.error)
         }

         if (!filesystem.existsSync(destFilepath)) {
             return err(new Error(`Your files were compressed but could not be found at '${destFilepath}'.`))
         }

         const stats = filesystem.statSync(destFilepath)
         if (stats.size >= GRPC_DATA_TRANSFER_LIMIT) {
             return err(new Error("The files you are trying to upload, which are now compressed, exceed or reach 4mb, " +
                 "a limit imposed by gRPC. Please reduce the total file size and ensure it can compress to a size below 4mb."))
         }

         if (stats.size <= 0) {
             return err(new Error("Something went wrong during compression. The compressed file size is 0 bytes."))
         }

         const data = filesystem.readFileSync(destFilepath)
         if(data.length != stats.size){
             return err(new Error(`Something went wrong while reading your recently compressed file '${destFilename}'.` +
                 `The file size of ${stats.size} bytes and read size of ${data.length} bytes are not equal.`))
         }

         return ok(new Uint8Array(data.buffer))
    }
}